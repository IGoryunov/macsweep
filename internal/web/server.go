// Package web serves the local browser UI: a loopback-only HTTP server with a
// random port and a per-session token, static files from embed.FS, and a small
// JSON/SSE API over the same core the CLI and TUI use.
package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/IGoryunov/macsweep/internal/analyze"
	"github.com/IGoryunov/macsweep/internal/clean"
	"github.com/IGoryunov/macsweep/internal/engine"
	"github.com/IGoryunov/macsweep/internal/policy"
	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/rules"
	"github.com/IGoryunov/macsweep/internal/scan"
	"github.com/IGoryunov/macsweep/internal/trash"
)

//go:embed static
var static embed.FS

// Deps is what the server needs from the core.
type Deps struct {
	Env       engine.Env
	Set       *rules.Set
	Groups    []string
	Workers   int
	Cached    map[string]scan.Result
	Analyzers []analyze.Analyzer
	Trasher   trash.Trasher
	Policy    *policy.Policy
	OnScan    func(rep *report.Report, measured []scan.Result)
	OnClean   func(j clean.Journal)
	// HeartbeatTimeout stops the server when the page has been silent this long
	// (after the first heartbeat). Zero disables the watchdog (tests).
	HeartbeatTimeout time.Duration
}

// Server is one browser session.
type Server struct {
	deps    Deps
	token   string
	scanner *scan.Scanner
	ctx     context.Context

	mu       sync.Mutex
	rep      *report.Report
	tree     *scan.Node
	scanJob  *job
	treeJob  *job
	lastBeat time.Time
	stop     func()
}

// job is a single-flight background task with progress polling.
type job struct {
	done chan struct{}
	err  error
}

// New creates a server; Start listens and returns the URL to open.
func New(ctx context.Context, deps Deps) (*Server, error) {
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	return &Server{
		deps: deps, token: hex.EncodeToString(tok), ctx: ctx,
		scanner: scan.New(scan.Options{Workers: deps.Workers}),
	}, nil
}

// Token returns the session token (for tests).
func (s *Server) Token() string { return s.token }

// Start listens on 127.0.0.1 with a random port and serves until ctx ends or
// the heartbeat watchdog fires. It returns the URL (with token) to open.
func (s *Server) Start(ctx context.Context) (string, <-chan error, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{Handler: s.Handler(ln.Addr().String()), ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(ctx)
	s.stop = cancel
	go func() {
		errCh <- srv.Serve(ln)
	}()
	go func() {
		<-ctx.Done()
		shutdown, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_ = srv.Shutdown(shutdown)
	}()
	if s.deps.HeartbeatTimeout > 0 {
		go s.watchdog(ctx, cancel)
	}
	url := fmt.Sprintf("http://%s/?token=%s", ln.Addr().String(), s.token)
	return url, errCh, nil
}

func (s *Server) watchdog(ctx context.Context, cancel func()) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	started := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			last := s.lastBeat
			s.mu.Unlock()
			if last.IsZero() {
				if time.Since(started) > 3*s.deps.HeartbeatTimeout {
					cancel() // browser never showed up
				}
				continue
			}
			if time.Since(last) > s.deps.HeartbeatTimeout {
				cancel()
			}
		}
	}
}

// Handler builds the mux. host is the expected Host header (anti-rebinding).
func (s *Server) Handler(host string) http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(static, "static")
	files := http.FileServer(http.FS(sub))
	mux.Handle("/", files)
	mux.HandleFunc("/api/scan", s.auth(s.handleScan))
	mux.HandleFunc("/api/candidates", s.auth(s.handleCandidates))
	mux.HandleFunc("/api/tree/build", s.auth(s.handleTreeBuild))
	mux.HandleFunc("/api/tree", s.auth(s.handleTree))
	mux.HandleFunc("/api/explain", s.auth(s.handleExplain))
	mux.HandleFunc("/api/trash", s.auth(s.handleTrash))
	mux.HandleFunc("/api/heartbeat", s.auth(s.handleHeartbeat))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host != "" && r.Host != host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") != s.token && r.URL.Query().Get("token") != s.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ---- scan ----

func (s *Server) startScan(rescan bool) *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scanJob != nil {
		select {
		case <-s.scanJob.done:
			if !rescan && s.rep != nil {
				return s.scanJob
			}
		default:
			return s.scanJob
		}
	}
	j := &job{done: make(chan struct{})}
	s.scanJob = j
	cached := s.deps.Cached
	if rescan {
		cached = nil
	}
	go func() {
		defer close(j.done)
		rep, measured, err := engine.Run(s.ctx, s.deps.Env, s.deps.Set, engine.Options{Groups: s.deps.Groups, Scanner: s.scanner, Cached: cached, Analyzers: s.deps.Analyzers})
		s.mu.Lock()
		if err == nil {
			s.rep = rep
		}
		j.err = err
		s.mu.Unlock()
		if err == nil && s.deps.OnScan != nil {
			s.deps.OnScan(rep, measured)
		}
	}()
	return j
}

// handleScan streams progress as SSE until the scan finishes.
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	j := s.startScan(r.URL.Query().Get("rescan") == "1")
	s.streamJob(w, r, j, "scan")
}

func (s *Server) streamJob(w http.ResponseWriter, r *http.Request, j *job, kind string) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		fl.Flush()
	}
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-j.done:
			if j.err != nil {
				send("error", map[string]string{"error": j.err.Error()})
			} else {
				send("done", map[string]string{"kind": kind})
			}
			return
		case <-t.C:
			p := s.scanner.Snapshot()
			send("progress", map[string]any{"paths": p.Paths, "bytes": p.Bytes, "current": report.DisplayPath(p.Current, s.deps.Env.Home)})
		}
	}
}

func (s *Server) handleCandidates(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	rep := s.rep
	s.mu.Unlock()
	if rep == nil {
		http.Error(w, "no scan yet", http.StatusConflict)
		return
	}
	writeJSON(w, rep)
}

// ---- tree ----

func (s *Server) startTree() *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.treeJob != nil {
		return s.treeJob
	}
	j := &job{done: make(chan struct{})}
	s.treeJob = j
	go func() {
		defer close(j.done)
		root, err := s.scanner.Tree(s.ctx, s.deps.Env.Home, scan.DefaultSkip(s.deps.Env.Home))
		s.mu.Lock()
		if err == nil {
			s.tree = root
		} else {
			s.treeJob = nil // allow retry
		}
		j.err = err
		s.mu.Unlock()
	}()
	return j
}

func (s *Server) handleTreeBuild(w http.ResponseWriter, r *http.Request) {
	s.streamJob(w, r, s.startTree(), "tree")
}

// treeNode is the JSON view of a subtree, depth-limited and trimmed.
type treeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Bytes    int64       `json:"bytes"`
	Files    int64       `json:"files"`
	Verdict  string      `json:"verdict,omitempty"`
	RuleID   string      `json:"rule_id,omitempty"`
	Children []*treeNode `json:"children,omitempty"`
	Other    int64       `json:"other,omitempty"`   // bytes of children not listed
	OtherN   int         `json:"other_n,omitempty"` // how many children were folded into Other
	Skipped  bool        `json:"skipped,omitempty"` // not mapped (cloud storage, other volume)
}

// handleTree returns the subtree at ?path (default home) to ?depth levels
// (default 5), listing at most ?limit children per node (default 64); the rest
// is folded into Other. It requires the full map (409 until built).
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	depth, _ := strconv.Atoi(q.Get("depth"))
	if depth <= 0 {
		depth = 5
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 64
	}
	path := q.Get("path")
	if path == "" {
		path = s.deps.Env.Home
	}
	s.mu.Lock()
	rep, tree := s.rep, s.tree
	s.mu.Unlock()
	if tree == nil {
		http.Error(w, "map not built yet", http.StatusConflict)
		return
	}
	verdicts := map[string]report.Candidate{}
	if rep != nil {
		for _, g := range rep.Groups {
			for _, c := range g.Candidates {
				verdicts[c.Path] = c
			}
		}
	}
	root := tree.Find(path)
	if root == nil {
		http.Error(w, "path not in map", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"full": true, "home_bytes": tree.Bytes, "home_files": tree.Files, "root": toTreeNode(root, depth, limit, verdicts, s.deps.Env.Home)})
}

func toTreeNode(n *scan.Node, depth, limit int, verdicts map[string]report.Candidate, home string) *treeNode {
	t := &treeNode{Name: filepath.Base(n.Path), Path: n.Path, Bytes: n.Bytes, Files: n.Files, Skipped: n.Skipped}
	if n.Path == home {
		t.Name = "~"
	}
	if c, ok := verdicts[n.Path]; ok {
		t.Verdict, t.RuleID = c.Verdict, c.RuleID
	}
	if depth == 0 {
		return t
	}
	for i, c := range n.Children {
		if i >= limit {
			t.Other += c.Bytes
			t.OtherN++
			continue
		}
		t.Children = append(t.Children, toTreeNode(c, depth-1, limit, verdicts, home))
	}
	return t
}

// ---- explain ----

func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	ex, err := engine.Explain(r.Context(), s.deps.Env, s.deps.Set, s.scanner, path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, ex)
}

// ---- trash ----

type trashRequest struct {
	Items []struct {
		Path        string    `json:"path"`
		Dev         uint64    `json:"dev"`
		Ino         uint64    `json:"ino"`
		RootModTime time.Time `json:"root_mod_time"`
	} `json:"items"`
}

// handleTrash moves the listed candidates to the Trash. Every item must match
// the current report's (dev, ino, mtime); the selected directories are
// re-measured and re-verified right before moving, exactly like --clean.
func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req trashRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	rep := s.rep
	s.mu.Unlock()
	if rep == nil {
		http.Error(w, "no scan yet", http.StatusConflict)
		return
	}
	if s.deps.Trasher == nil {
		http.Error(w, "trash unavailable", http.StatusServiceUnavailable)
		return
	}
	byPath := map[string]*report.Candidate{}
	for gi := range rep.Groups {
		for ci := range rep.Groups[gi].Candidates {
			c := &rep.Groups[gi].Candidates[ci]
			byPath[c.Path] = c
		}
	}
	var paths []string
	for _, it := range req.Items {
		c, ok := byPath[it.Path]
		if !ok {
			http.Error(w, "not a candidate: "+it.Path, http.StatusBadRequest)
			return
		}
		if c.Dev != it.Dev || c.Ino != it.Ino || !c.RootModTime.Equal(it.RootModTime) {
			http.Error(w, "stale item (rescan first): "+it.Path, http.StatusConflict)
			return
		}
		paths = append(paths, it.Path)
	}
	fresh := *rep
	fresh.Groups = make([]report.Group, len(rep.Groups))
	for gi := range rep.Groups {
		cands := make([]report.Candidate, len(rep.Groups[gi].Candidates))
		copy(cands, rep.Groups[gi].Candidates)
		for ci := range cands {
			c := &cands[ci]
			for _, p := range paths {
				if c.Path == p {
					if res, err := s.scanner.Measure(r.Context(), c.Path); err == nil {
						c.Bytes, c.Files, c.Dev, c.Ino, c.RootModTime = res.Bytes, res.Files, res.Dev, res.Ino, res.ModTime
					}
				}
			}
		}
		fresh.Groups[gi] = report.Group{Name: rep.Groups[gi].Name, Candidates: cands}
	}
	plan, err := clean.BuildPlan(&fresh, paths)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	j := clean.Execute(r.Context(), plan, s.deps.Trasher, s.deps.Policy, s.deps.Env.Home)
	if s.deps.OnClean != nil {
		s.deps.OnClean(j)
	}
	// Drop trashed candidates from the report so the UI reflects reality.
	trashed := map[string]bool{}
	for _, o := range j.Items {
		if o.Status == "trashed" || o.Status == "contained" {
			trashed[o.Path] = true
		}
	}
	s.mu.Lock()
	for gi := range s.rep.Groups {
		g := &s.rep.Groups[gi]
		kept := g.Candidates[:0]
		for _, c := range g.Candidates {
			if !trashed[c.Path] {
				kept = append(kept, c)
			}
		}
		g.Candidates = kept
	}
	s.tree = nil // the map is stale now
	s.treeJob = nil
	s.mu.Unlock()
	writeJSON(w, j)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.lastBeat = time.Now()
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// ErrServerClosed is what Start's error channel yields after a clean shutdown.
var ErrServerClosed = http.ErrServerClosed
