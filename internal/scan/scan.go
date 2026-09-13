// Package scan measures directory trees in parallel. It knows nothing about
// rules or verdicts: give it a root, it returns allocated bytes, counts,
// the newest mtime and the multi-link inodes it saw.
package scan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// ErrSymlinkRoot is returned when the root itself is a symbolic link.
var ErrSymlinkRoot = errors.New("root is a symbolic link")

// Options configures a Scanner.
type Options struct {
	Workers  int             // 0 → min(NumCPU*2, 16)
	Progress chan<- Progress // optional; receives snapshots every 100 ms
}

// Progress is a snapshot of scan activity.
type Progress struct {
	Paths   int64
	Bytes   int64
	Current string
}

// InodeKey identifies a file across hardlinks.
type InodeKey struct{ Dev, Ino uint64 }

// Result is the measurement of one root.
type Result struct {
	Path       string             `json:"path"`
	Dev        uint64             `json:"dev"`
	Ino        uint64             `json:"ino"`
	ModTime    time.Time          `json:"mod_time"`     // of the root itself
	MaxModTime time.Time          `json:"max_mod_time"` // newest entry inside (or root)
	Bytes      int64              `json:"bytes"`        // allocated bytes, multi-link inodes once
	Files      int64              `json:"files"`
	Dirs       int64              `json:"dirs"`
	Links      map[InodeKey]int64 `json:"-"` // multi-link inodes and their bytes
	LinkList   []LinkEntry        `json:"links,omitempty"`
	Errors     []string           `json:"errors,omitempty"`
}

// LinkEntry is the JSON form of one Links entry.
type LinkEntry struct {
	Dev   uint64 `json:"dev"`
	Ino   uint64 `json:"ino"`
	Bytes int64  `json:"bytes"`
}

// FreezeLinks copies Links into LinkList for serialisation.
func (r *Result) FreezeLinks() {
	r.LinkList = r.LinkList[:0]
	for k, v := range r.Links {
		r.LinkList = append(r.LinkList, LinkEntry{k.Dev, k.Ino, v})
	}
}

// ThawLinks rebuilds Links from LinkList after deserialisation.
func (r *Result) ThawLinks() {
	r.Links = make(map[InodeKey]int64, len(r.LinkList))
	for _, e := range r.LinkList {
		r.Links[InodeKey{e.Dev, e.Ino}] = e.Bytes
	}
}

// Scanner owns a bounded worker pool shared by all measurements in a run.
type Scanner struct {
	sem      chan struct{}
	progress chan<- Progress
	paths    atomic.Int64
	bytes    atomic.Int64
	current  atomic.Pointer[string]
}

// New creates a Scanner.
func New(o Options) *Scanner {
	w := o.Workers
	if w <= 0 {
		w = runtime.NumCPU() * 2
		if w > 16 {
			w = 16
		}
	}
	return &Scanner{sem: make(chan struct{}, w), progress: o.Progress}
}

// Snapshot returns the current progress counters.
func (s *Scanner) Snapshot() Progress {
	p := Progress{Paths: s.paths.Load(), Bytes: s.bytes.Load()}
	if c := s.current.Load(); c != nil {
		p.Current = *c
	}
	return p
}

// WithProgress runs fn while emitting progress snapshots every 100 ms.
func (s *Scanner) WithProgress(fn func()) {
	if s.progress == nil {
		fn()
		return
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				s.emit()
			}
		}
	}()
	fn()
	close(done)
	s.emit()
}

func (s *Scanner) emit() {
	select {
	case s.progress <- s.Snapshot():
	default:
	}
}

const maxErrors = 20

type acc struct {
	root    string
	rootDev uint64
	bytes   atomic.Int64
	files   atomic.Int64
	dirs    atomic.Int64
	maxMod  atomic.Int64 // unix nanos
	mu      sync.Mutex
	links   map[InodeKey]int64
	errs    []string
	nerrs   int
	wg      sync.WaitGroup
}

func (a *acc) addErr(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nerrs++
	if len(a.errs) < maxErrors {
		a.errs = append(a.errs, err.Error())
	}
}

func (a *acc) bumpMod(t time.Time) {
	n := t.UnixNano()
	for {
		cur := a.maxMod.Load()
		if n <= cur || a.maxMod.CompareAndSwap(cur, n) {
			return
		}
	}
}

// Measure walks root and returns its measurement. On cancellation the partial
// result is returned together with ctx.Err().
func (s *Scanner) Measure(ctx context.Context, root string) (Result, error) {
	fi, err := os.Lstat(root)
	if err != nil {
		return Result{Path: root}, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return Result{Path: root}, ErrSymlinkRoot
	}
	dev, ino, nlink, bytes, _ := statOf(fi)
	a := &acc{root: root, rootDev: dev, links: map[InodeKey]int64{}}
	a.bytes.Add(bytes)
	a.bumpMod(fi.ModTime())
	s.paths.Add(1)
	s.bytes.Add(bytes)
	if fi.IsDir() {
		a.dirs.Add(1)
		s.walkDir(ctx, root, a)
		a.wg.Wait()
	} else {
		a.files.Add(1)
		if nlink > 1 {
			a.links[InodeKey{dev, ino}] = bytes
		}
	}
	res := Result{
		Path: root, Dev: dev, Ino: ino, ModTime: fi.ModTime(),
		MaxModTime: time.Unix(0, a.maxMod.Load()),
		Bytes:      a.bytes.Load(), Files: a.files.Load(), Dirs: a.dirs.Load(),
		Links: a.links, Errors: a.errs,
	}
	if a.nerrs > maxErrors {
		res.Errors = append(res.Errors, fmt.Sprintf("... and %d more errors", a.nerrs-maxErrors))
	}
	return res, ctx.Err()
}

func (s *Scanner) walkDir(ctx context.Context, dir string, a *acc) {
	if ctx.Err() != nil {
		return
	}
	s.current.Store(&dir)
	f, err := os.Open(dir)
	if err != nil {
		a.addErr(err)
		return
	}
	defer f.Close()
	for {
		if ctx.Err() != nil {
			return
		}
		ents, err := f.ReadDir(256)
		for _, d := range ents {
			if ctx.Err() != nil {
				return
			}
			s.entry(ctx, dir, d, a)
		}
		if err != nil {
			if !errors.Is(err, os.ErrClosed) && err.Error() != "EOF" {
				a.addErr(err)
			}
			return
		}
		if len(ents) == 0 {
			return
		}
	}
}

func (s *Scanner) entry(ctx context.Context, dir string, d fs.DirEntry, a *acc) {
	fi, err := d.Info() // lstat; never follows symlinks
	if err != nil {
		a.addErr(err)
		return
	}
	dev, ino, nlink, bytes, _ := statOf(fi)
	s.paths.Add(1)
	a.bumpMod(fi.ModTime())
	counted := true
	if nlink > 1 && !fi.IsDir() {
		a.mu.Lock()
		if _, seen := a.links[InodeKey{dev, ino}]; seen {
			counted = false
		} else {
			a.links[InodeKey{dev, ino}] = bytes
		}
		a.mu.Unlock()
	}
	if counted {
		a.bytes.Add(bytes)
		s.bytes.Add(bytes)
	}
	if !fi.IsDir() {
		a.files.Add(1)
		return
	}
	a.dirs.Add(1)
	if dev != a.rootDev {
		return // mount point or firmlink: count the entry, do not descend
	}
	sub := filepath.Join(dir, d.Name())
	a.wg.Add(1)
	select {
	case s.sem <- struct{}{}:
		go func() {
			defer a.wg.Done()
			defer func() { <-s.sem }()
			s.walkDir(ctx, sub, a)
		}()
	default:
		s.walkDir(ctx, sub, a)
		a.wg.Done()
	}
}
