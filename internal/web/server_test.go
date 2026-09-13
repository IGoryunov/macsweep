package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/IGoryunov/macsweep/internal/clean"
	"github.com/IGoryunov/macsweep/internal/engine"
	"github.com/IGoryunov/macsweep/internal/policy"
	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/rules"
	"github.com/IGoryunov/macsweep/internal/testutil/fixture"
)

type fakeTrash struct{ dir string }

func (f fakeTrash) Trash(p string) (string, error) {
	dest := filepath.Join(f.dir, filepath.Base(p))
	return dest, os.Rename(p, dest)
}

const testRules = `
version: 1
group: T
rules:
  - id: caches
    glob: "$CA/*"
    prune: true
    verdict: safe
    note: cache
    recovery: rebuilt
  - id: dl
    paths: ["~/Downloads"]
    verdict: keep
    note: yours
    recovery: n/a
`

func newTestServer(t *testing.T) (*Server, string, *httptest.Server) {
	t.Helper()
	home := fixture.Build(t, fixture.Tree{
		"Library/Caches/A/f":   fixture.File{Size: 4096},
		"Library/Caches/B/f":   fixture.File{Size: 8192},
		"Downloads/f":          fixture.File{Size: 4096},
		"Documents/big/f":      fixture.File{Size: 4096},
		"Library/Caches/B/g/h": fixture.File{Size: 4096},
	})
	set, err := rules.Load(fstest.MapFS{"t.yaml": {Data: []byte(testRules)}})
	if err != nil {
		t.Fatal(err)
	}
	var journals []clean.Journal
	s, err := New(context.Background(), Deps{
		Env:     engine.Env{Home: home, Now: time.Now, Policy: policy.New(home)},
		Set:     set,
		Trasher: fakeTrash{dir: t.TempDir()},
		Policy:  policy.New(home),
		OnClean: func(j clean.Journal) { journals = append(journals, j) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler(""))
	t.Cleanup(ts.Close)
	return s, home, ts
}

func get(t *testing.T, ts *httptest.Server, s *Server, path string, out any) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	req.Header.Set("X-Token", s.token)
	r, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil && r.StatusCode == 200 {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func waitSSE(t *testing.T, ts *httptest.Server, s *Server, path string) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	req.Header.Set("X-Token", s.token)
	r, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	sc := bufio.NewScanner(r.Body)
	for sc.Scan() {
		line := sc.Text()
		if line == "event: done" {
			return
		}
		if line == "event: error" {
			sc.Scan()
			t.Fatalf("job failed: %s", sc.Text())
		}
	}
	t.Fatal("stream ended without done")
}

func TestAuthAndFlow(t *testing.T) {
	s, home, ts := newTestServer(t)

	// No token → 401; static index needs no token.
	if r, _ := ts.Client().Get(ts.URL + "/api/candidates"); r.StatusCode != 401 {
		t.Fatalf("unauthenticated API returned %d", r.StatusCode)
	}
	if r, _ := ts.Client().Get(ts.URL + "/"); r.StatusCode != 200 || r.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("index: %d csp=%q", r.StatusCode, r.Header.Get("Content-Security-Policy"))
	}
	// Candidates before a scan → 409.
	if r := get(t, ts, s, "/api/candidates", nil); r.StatusCode != 409 {
		t.Fatalf("candidates before scan: %d", r.StatusCode)
	}

	waitSSE(t, ts, s, "/api/scan")
	var rep report.Report
	get(t, ts, s, "/api/candidates", &rep)
	if len(rep.Groups) != 1 || len(rep.Groups[0].Candidates) != 3 {
		t.Fatalf("candidates: %+v", rep.Groups)
	}

	// No tree before the full map is built.
	var tr struct {
		Full bool      `json:"full"`
		Root *treeNode `json:"root"`
	}
	if r := get(t, ts, s, "/api/tree", nil); r.StatusCode != 409 {
		t.Fatalf("tree before map: %d", r.StatusCode)
	}
	waitSSE(t, ts, s, "/api/tree/build")
	get(t, ts, s, "/api/tree?depth=2", &tr)
	if !tr.Full || len(tr.Root.Children) != 3 { // Library, Downloads, Documents
		t.Fatalf("full tree: %+v", tr.Root)
	}
	var sub struct {
		Root *treeNode `json:"root"`
	}
	get(t, ts, s, "/api/tree?path="+filepath.Join(home, "Library/Caches"), &sub)
	if sub.Root.Name != "Caches" || len(sub.Root.Children) != 2 || sub.Root.Children[0].Verdict != "safe" {
		t.Fatalf("subtree: %+v", sub.Root)
	}
	get(t, ts, s, "/api/tree?path="+filepath.Join(home, "Library/Caches")+"&limit=1", &sub)
	if len(sub.Root.Children) != 1 || sub.Root.OtherN != 1 || sub.Root.Other != 4096 {
		t.Fatalf("other folding: %+v", sub.Root)
	}
	if r := get(t, ts, s, "/api/tree?path=/nope", nil); r.StatusCode != 404 {
		t.Fatalf("unknown path: %d", r.StatusCode)
	}

	// Explain.
	var ex report.Explanation
	get(t, ts, s, "/api/explain?path="+filepath.Join(home, "Documents"), &ex)
	if ex.Allowed {
		t.Fatal("Documents should be denied")
	}

	// Trash: stale item rejected, keep rejected, good item moved.
	var a, keep report.Candidate
	for _, c := range rep.Groups[0].Candidates {
		switch filepath.Base(c.Path) {
		case "A":
			a = c
		case "Downloads":
			keep = c
		}
	}
	post := func(items []map[string]any) *http.Response {
		body, _ := json.Marshal(map[string]any{"items": items})
		req, _ := http.NewRequest("POST", ts.URL+"/api/trash", strings.NewReader(string(body)))
		req.Header.Set("X-Token", s.token)
		req.Header.Set("Content-Type", "application/json")
		r, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	item := func(c report.Candidate, ino uint64) map[string]any {
		return map[string]any{"path": c.Path, "dev": c.Dev, "ino": ino, "root_mod_time": c.RootModTime}
	}
	if r := post([]map[string]any{item(a, a.Ino+1)}); r.StatusCode != 409 {
		t.Fatalf("stale item: %d", r.StatusCode)
	}
	if r := post([]map[string]any{item(keep, keep.Ino)}); r.StatusCode != 400 {
		t.Fatalf("keep item: %d", r.StatusCode)
	}
	r := post([]map[string]any{item(a, a.Ino)})
	if r.StatusCode != 200 {
		t.Fatalf("trash: %d", r.StatusCode)
	}
	var j clean.Journal
	_ = json.NewDecoder(r.Body).Decode(&j)
	if j.Trashed != 1 || j.Skipped != 0 {
		t.Fatalf("journal: %+v", j)
	}
	if _, err := os.Lstat(a.Path); !os.IsNotExist(err) {
		t.Fatal("A should be gone")
	}
	get(t, ts, s, "/api/candidates", &rep)
	if len(rep.Groups[0].Candidates) != 2 {
		t.Fatalf("trashed candidate still listed: %+v", rep.Groups[0].Candidates)
	}
	// Heartbeat.
	req, _ := http.NewRequest("POST", ts.URL+"/api/heartbeat", nil)
	req.Header.Set("X-Token", s.token)
	if hr, _ := ts.Client().Do(req); hr.StatusCode != 204 {
		t.Fatalf("heartbeat: %d", hr.StatusCode)
	}
}

func TestHostCheck(t *testing.T) {
	s, _, _ := newTestServer(t)
	h := s.Handler("127.0.0.1:1234")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://evil.example/", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("wrong host accepted: %d", rr.Code)
	}
}
