package scan

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
)

// Node is one directory in a size tree.
type Node struct {
	Path     string
	Bytes    int64 // allocated bytes of the whole subtree
	Files    int64
	Children []*Node // directories only, sorted by Bytes descending
	Skipped  bool    // not entered (cloud storage mount, other device)
}

// DefaultSkip returns the skip function used for whole-home maps: cloud
// storage mounts under ~/Library/CloudStorage are file-provider volumes whose
// contents live in the cloud, not on this disk, and walking them is slow.
func DefaultSkip(home string) func(abs string) bool {
	cloud := filepath.Join(home, "Library", "CloudStorage")
	return func(abs string) bool { return abs == cloud }
}

// Tree walks root and returns a per-directory size tree. Symlinks are never
// followed, other devices are not entered, and multi-link inodes are counted
// once per tree (attributed to whichever directory saw them first). skip, when
// non-nil, prevents entering a directory (it is still listed with zero size).
func (s *Scanner) Tree(ctx context.Context, root string, skip func(abs string) bool) (*Node, error) {
	fi, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, ErrSymlinkRoot
	}
	dev, _, _, _, _ := statOf(fi)
	n := &Node{Path: root}
	var mu sync.Mutex
	seen := map[InodeKey]bool{}
	s.treeWalk(ctx, n, dev, skip, &mu, seen)
	return n, ctx.Err()
}

func (s *Scanner) treeWalk(ctx context.Context, n *Node, rootDev uint64, skip func(string) bool, mu *sync.Mutex, seen map[InodeKey]bool) {
	if ctx.Err() != nil {
		return
	}
	s.current.Store(&n.Path)
	f, err := os.Open(n.Path)
	if err != nil {
		return
	}
	var own, files atomic.Int64
	var wg sync.WaitGroup
	var cmu sync.Mutex
	for ctx.Err() == nil {
		ents, err := f.ReadDir(256)
		for _, d := range ents {
			fi, err := d.Info()
			if err != nil {
				continue
			}
			dev, ino, nlink, bytes, _ := statOf(fi)
			s.paths.Add(1)
			if nlink > 1 && !fi.IsDir() {
				mu.Lock()
				dup := seen[InodeKey{dev, ino}]
				if !dup {
					seen[InodeKey{dev, ino}] = true
				}
				mu.Unlock()
				if dup {
					bytes = 0
				}
			}
			own.Add(bytes)
			s.bytes.Add(bytes)
			if !fi.IsDir() {
				files.Add(1)
				continue
			}
			// The directory entry's own blocks were added to `own` above; the child
			// node accounts only for what is inside it.
			child := &Node{Path: filepath.Join(n.Path, d.Name())}
			cmu.Lock()
			n.Children = append(n.Children, child)
			cmu.Unlock()
			if dev != rootDev || (skip != nil && skip(child.Path)) {
				child.Skipped = true
				continue
			}
			wg.Add(1)
			select {
			case s.sem <- struct{}{}:
				go func() {
					defer wg.Done()
					defer func() { <-s.sem }()
					s.treeWalk(ctx, child, rootDev, skip, mu, seen)
				}()
			default:
				s.treeWalk(ctx, child, rootDev, skip, mu, seen)
				wg.Done()
			}
		}
		if err != nil || len(ents) == 0 {
			break
		}
	}
	f.Close()
	wg.Wait()
	total, nfiles := own.Load(), files.Load()
	for _, c := range n.Children {
		total += c.Bytes
		nfiles += c.Files
	}
	sort.Slice(n.Children, func(i, j int) bool { return n.Children[i].Bytes > n.Children[j].Bytes })
	n.Bytes, n.Files = total, nfiles
}

// Find returns the node with the given path, or nil.
func (n *Node) Find(path string) *Node {
	if n.Path == path {
		return n
	}
	for _, c := range n.Children {
		if path == c.Path || len(path) > len(c.Path) && path[:len(c.Path)] == c.Path && path[len(c.Path)] == '/' {
			return c.Find(path)
		}
	}
	return nil
}
