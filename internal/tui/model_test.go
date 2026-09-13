package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/IGoryunov/macsweep/internal/clean"
	"github.com/IGoryunov/macsweep/internal/engine"
	"github.com/IGoryunov/macsweep/internal/policy"
	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/scan"
)

type fakeTrash struct{ dir string }

func (f fakeTrash) Trash(p string) (string, error) {
	dest := filepath.Join(f.dir, filepath.Base(p))
	return dest, os.Rename(p, dest)
}

func key(s string) tea.KeyMsg {
	switch s {
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func step(t *testing.T, m tea.Model, msgs ...tea.Msg) (model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		m, cmd = m.Update(msg)
	}
	return m.(model), cmd
}

func TestResultsFlow(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	victim := filepath.Join(home, "Library", "Caches", "Foo")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := scan.New(scan.Options{}).Measure(context.Background(), victim)
	rep := testReport()
	rep.Groups = append(rep.Groups, report.Group{Name: "Real", Candidates: []report.Candidate{{
		Path: victim, DisplayPath: "~/Library/Caches/Foo", Bytes: res.Bytes, Verdict: "safe",
		Dev: res.Dev, Ino: res.Ino, RootModTime: res.ModTime, Note: "n", Recovery: "r", Reasons: []string{"rule default: n"},
	}}})
	rep.Home = home

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := model{
		deps: Deps{Env: engine.Env{Home: home, Policy: policy.New(home)}, Trasher: fakeTrash{dir: t.TempDir()}, Policy: policy.New(home)},
		ctx:  ctx, cancel: cancel, scanner: scan.New(scan.Options{}),
		collapsed: map[string]bool{}, sel: selection{}, width: 100, height: 30,
	}
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 30}, scanDoneMsg{rep: rep})
	if m.screen != screenResults || len(m.rows) != 9 {
		t.Fatalf("after scan: screen=%d rows=%d", m.screen, len(m.rows))
	}
	if v := m.View(); !strings.Contains(v, "▾ A") || !strings.Contains(v, "~/a2") {
		t.Fatalf("results view:\n%s", v)
	}
	// Fold group A, move to B header, unfold via tab, mark the Real item.
	m, _ = step(t, m, key("tab"))
	if len(m.rows) != 5 {
		t.Fatalf("fold: %d rows", len(m.rows))
	}
	m, _ = step(t, m, key("G"), key("space")) // cursor on last row = Real candidate
	if !m.sel[victim] {
		t.Fatalf("space did not mark: cursor=%d sel=%v", m.cursor, m.sel)
	}
	// Unfold A again, then filter narrows rows; esc clears.
	m, _ = step(t, m, key("g"), key("tab"))
	m, _ = step(t, m, key("/"), key("a2"), tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.rows) != 2 || m.filter != "a2" {
		t.Fatalf("filter: rows=%d filter=%q", len(m.rows), m.filter)
	}
	m, _ = step(t, m, key("/"), key("esc"))
	if m.filter != "" {
		t.Fatal("esc should clear the filter")
	}
	// Mark a review item too, then d → confirm needs two y's.
	m, _ = step(t, m, key("g"), key("down"), key("space")) // A header → a2 (review)
	if !m.sel["/h/a2"] {
		t.Fatalf("review mark failed: cursor=%d rows[1]=%+v", m.cursor, m.rows[1])
	}
	m, _ = step(t, m, key("d"))
	if m.screen != screenConfirm || !strings.Contains(m.View(), "review") {
		t.Fatalf("confirm screen: %d\n%s", m.screen, m.View())
	}
	m, _ = step(t, m, key("y"))
	if m.confirmStage != 1 || m.screen != screenConfirm {
		t.Fatal("first y must ask again when review items are marked")
	}
	m, cmd := step(t, m, key("y"))
	if m.screen != screenCleaning || cmd == nil {
		t.Fatal("second y must start cleaning")
	}
	msg := cmd().(cleanDoneMsg)
	m, _ = step(t, m, msg)
	if m.screen != screenDone {
		t.Fatal("expected done screen")
	}
	// /h/a2 does not exist → skipped; the real dir → trashed via fake.
	if msg.j.Trashed != 1 || msg.j.Skipped != 1 || m.exitCode != 2 {
		t.Fatalf("journal: %+v exit=%d", msg.j, m.exitCode)
	}
	if _, err := os.Lstat(victim); !os.IsNotExist(err) {
		t.Error("victim should have been moved")
	}
	if !strings.Contains(m.View(), "skipped 1") {
		t.Errorf("done view:\n%s", m.View())
	}
	_ = clean.JournalVersion
}
