package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/IGoryunov/macsweep/internal/analyze"
	"github.com/IGoryunov/macsweep/internal/clean"
	"github.com/IGoryunov/macsweep/internal/engine"
	"github.com/IGoryunov/macsweep/internal/policy"
	"github.com/IGoryunov/macsweep/internal/report"
	"github.com/IGoryunov/macsweep/internal/rules"
	"github.com/IGoryunov/macsweep/internal/scan"
	"github.com/IGoryunov/macsweep/internal/trash"
)

// Deps is everything the TUI needs from the core.
type Deps struct {
	Env       engine.Env
	Set       *rules.Set
	Groups    []string
	Workers   int
	Cached    map[string]scan.Result
	Analyzers []analyze.Analyzer
	Trasher   trash.Trasher
	Policy    *policy.Policy
	OnScan    func(rep *report.Report, measured []scan.Result) // e.g. save the manifest
	OnClean   func(j clean.Journal)                            // e.g. save the journal
	Progress  chan scan.Progress
}

type screen int

const (
	screenScan screen = iota
	screenResults
	screenExplain
	screenConfirm
	screenCleaning
	screenDone
)

type (
	progressMsg scan.Progress
	scanDoneMsg struct {
		rep      *report.Report
		measured []scan.Result
		err      error
	}
	explainMsg struct {
		ex  *report.Explanation
		err error
	}
	cleanDoneMsg struct{ j clean.Journal }
)

type model struct {
	deps    Deps
	ctx     context.Context
	cancel  context.CancelFunc
	scanner *scan.Scanner

	screen   screen
	width    int
	height   int
	spin     spinner.Model
	progress scan.Progress
	rep      *report.Report
	err      error

	rows      []row
	cursor    int
	offset    int
	collapsed map[string]bool
	sel       selection
	filter    string
	filtering bool
	showHelp  bool

	explanation *report.Explanation
	explainErr  error
	explainPath string

	confirmStage int // 0 = first prompt, 1 = review items need a second yes
	journal      clean.Journal
	exitCode     int
}

// Run drives the whole interactive session and returns the process exit code.
func Run(ctx context.Context, deps Deps) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if deps.Progress == nil {
		deps.Progress = make(chan scan.Progress, 16)
	}
	m := model{
		deps: deps, ctx: ctx, cancel: cancel,
		scanner:   scan.New(scan.Options{Workers: deps.Workers, Progress: deps.Progress}),
		spin:      spinner.New(spinner.WithSpinner(spinner.Dot)),
		collapsed: map[string]bool{},
		sel:       selection{},
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return 1, err
	}
	fm := final.(model)
	if fm.err != nil {
		return 1, fm.err
	}
	return fm.exitCode, nil
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.startScan(), m.waitProgress())
}

func (m model) startScan() tea.Cmd {
	return func() tea.Msg {
		var rep *report.Report
		var measured []scan.Result
		var err error
		m.scanner.WithProgress(func() {
			rep, measured, err = engine.Run(m.ctx, m.deps.Env, m.deps.Set, engine.Options{Groups: m.deps.Groups, Scanner: m.scanner, Cached: m.deps.Cached, Analyzers: m.deps.Analyzers})
		})
		return scanDoneMsg{rep, measured, err}
	}
}

func (m model) waitProgress() tea.Cmd {
	return func() tea.Msg {
		p, ok := <-m.deps.Progress
		if !ok {
			return nil
		}
		return progressMsg(p)
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case progressMsg:
		m.progress = scan.Progress(msg)
		return m, m.waitProgress()
	case scanDoneMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.rep = msg.rep
		if m.deps.OnScan != nil {
			m.deps.OnScan(msg.rep, msg.measured)
		}
		m.screen = screenResults
		m.rows = buildRows(m.rep, m.collapsed, m.filter)
		return m, nil
	case explainMsg:
		m.explanation, m.explainErr = msg.ex, msg.err
		return m, nil
	case cleanDoneMsg:
		m.journal = msg.j
		if m.deps.OnClean != nil {
			m.deps.OnClean(msg.j)
		}
		m.screen = screenDone
		if msg.j.Skipped > 0 || msg.j.Failed > 0 {
			m.exitCode = 2
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.Type == tea.KeyCtrlC {
		m.cancel()
		if m.screen == screenScan || m.screen == screenCleaning {
			m.exitCode = 1
		}
		return m, tea.Quit
	}
	switch m.screen {
	case screenScan:
		if k.String() == "q" {
			m.cancel()
			m.exitCode = 1
			return m, tea.Quit
		}
	case screenResults:
		return m.keyResults(k)
	case screenExplain:
		switch k.String() {
		case "esc", "q", "e", "enter":
			m.screen = screenResults
		}
	case screenConfirm:
		return m.keyConfirm(k)
	case screenDone:
		switch k.String() {
		case "q", "enter", "esc":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) keyResults(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		switch k.Type {
		case tea.KeyEnter, tea.KeyEsc:
			m.filtering = false
			if k.Type == tea.KeyEsc {
				m.filter = ""
			}
		case tea.KeyBackspace:
			if len(m.filter) > 0 {
				r := []rune(m.filter)
				m.filter = string(r[:len(r)-1])
			}
		case tea.KeyRunes:
			m.filter += string(k.Runes)
		}
		m.rows = buildRows(m.rep, m.collapsed, m.filter)
		m.clampCursor()
		return m, nil
	}
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "?":
		m.showHelp = true
	case "up", "k":
		m.cursor--
	case "down", "j":
		m.cursor++
	case "pgup":
		m.cursor -= m.listHeight()
	case "pgdown":
		m.cursor += m.listHeight()
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.rows) - 1
	case " ":
		if r := m.current(); r != nil {
			if r.header {
				m.sel.toggleGroup(m.rows, r.group)
			} else {
				m.sel.toggle(r.cand)
			}
		}
	case "a":
		if r := m.current(); r != nil {
			m.sel.toggleGroup(m.rows, r.group)
		}
	case "tab", "enter":
		if r := m.current(); r != nil {
			m.collapsed[r.group] = !m.collapsed[r.group]
			m.rows = buildRows(m.rep, m.collapsed, m.filter)
		}
	case "/":
		m.filtering = true
	case "e":
		if r := m.current(); r != nil && !r.header {
			m.screen = screenExplain
			m.explanation, m.explainErr, m.explainPath = nil, nil, r.cand.Path
			path := r.cand.Path
			return m, func() tea.Msg {
				ex, err := engine.Explain(m.ctx, m.deps.Env, m.deps.Set, m.scanner, path)
				return explainMsg{ex, err}
			}
		}
	case "d":
		if len(m.sel) > 0 {
			m.screen = screenConfirm
			m.confirmStage = 0
		}
	}
	m.clampCursor()
	return m, nil
}

func (m model) keyConfirm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "n", "esc", "q":
		m.screen = screenResults
	case "y":
		s := m.sel.summarize(m.rep)
		if s.review > 0 && m.confirmStage == 0 {
			m.confirmStage = 1
			return m, nil
		}
		m.screen = screenCleaning
		return m, m.startClean()
	}
	return m, nil
}

// startClean re-measures the selected candidates (fresh inode/mtime) and runs
// the same verified trash pipeline as --clean.
func (m model) startClean() tea.Cmd {
	paths := make([]string, 0, len(m.sel))
	for p := range m.sel {
		paths = append(paths, p)
	}
	rep := m.rep
	return func() tea.Msg {
		fresh := *rep
		fresh.Groups = make([]report.Group, len(rep.Groups))
		copy(fresh.Groups, rep.Groups)
		for gi := range fresh.Groups {
			cands := make([]report.Candidate, len(fresh.Groups[gi].Candidates))
			copy(cands, fresh.Groups[gi].Candidates)
			for ci := range cands {
				c := &cands[ci]
				if !m.sel[c.Path] {
					continue
				}
				if res, err := m.scanner.Measure(m.ctx, c.Path); err == nil {
					c.Bytes, c.Files, c.Dev, c.Ino, c.RootModTime, c.ModTime = res.Bytes, res.Files, res.Dev, res.Ino, res.ModTime, res.MaxModTime
				}
			}
			fresh.Groups[gi].Candidates = cands
		}
		plan, err := clean.BuildPlan(&fresh, paths)
		if err != nil {
			return cleanDoneMsg{clean.Journal{Version: clean.JournalVersion, Home: m.deps.Env.Home, Items: []clean.Outcome{{Status: "failed", Reason: err.Error()}}, Failed: 1}}
		}
		return cleanDoneMsg{clean.Execute(m.ctx, plan, m.deps.Trasher, m.deps.Policy, m.deps.Env.Home)}
	}
}

func (m *model) clampCursor() {
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > len(m.rows)-1 {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	h := m.listHeight()
	if h < 1 {
		h = 1
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
}

func (m model) current() *row {
	if len(m.rows) == 0 || m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return &m.rows[m.cursor]
}

// listHeight is the number of list lines: total minus title, status, detail panel and help.
func (m model) listHeight() int {
	h := m.height - 2 - detailLines - 2
	if h < 3 {
		h = 3
	}
	return h
}

const detailLines = 7

// ---- views ----

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	safeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	reviewStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	cursorStyle = lipgloss.NewStyle().Reverse(true)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
)

func (m model) View() string {
	switch m.screen {
	case screenScan:
		return m.viewScan()
	case screenResults:
		return m.viewResults()
	case screenExplain:
		return m.viewExplain()
	case screenConfirm:
		return m.viewConfirm()
	case screenCleaning:
		return fmt.Sprintf("\n  %s Moving items to the Trash…\n\n  %s\n", m.spin.View(), dimStyle.Render("Each item is re-checked immediately before it moves."))
	case screenDone:
		return m.viewDone()
	}
	return ""
}

func (m model) viewScan() string {
	cur := report.DisplayPath(m.progress.Current, m.deps.Env.Home)
	if w := m.width - 14; w > 10 && len(cur) > w {
		cur = "…" + cur[len(cur)-w+1:]
	}
	return fmt.Sprintf("\n  %s %s\n\n  %s paths, %s\n  %s\n\n  %s\n",
		m.spin.View(), titleStyle.Render("Scanning…"),
		fmtInt(m.progress.Paths), report.HumanSize(m.progress.Bytes),
		dimStyle.Render(cur),
		dimStyle.Render("q or Ctrl-C cancels"))
}

func (m model) viewResults() string {
	var b strings.Builder
	s := m.sel.summarize(m.rep)
	title := fmt.Sprintf("macsweep  ✓ %s safe   ? %s review   marked: %d (%s)",
		report.HumanSize(m.rep.Totals.SafeBytes), report.HumanSize(m.rep.Totals.ReviewBytes), s.count, report.HumanSize(s.bytes))
	if m.rep.FromCache {
		title += dimStyle.Render("  [cached sizes]")
	}
	b.WriteString(titleStyle.Render(title) + "\n")
	if m.filtering || m.filter != "" {
		b.WriteString("filter: " + m.filter + map[bool]string{true: "▌", false: ""}[m.filtering] + "\n")
	} else {
		b.WriteString(dimStyle.Render("↑↓ move  space mark  a group  tab fold  e explain  / filter  d trash marked  ? help  q quit") + "\n")
	}
	h := m.listHeight()
	if len(m.rows) == 0 {
		b.WriteString(dimStyle.Render("\n  nothing matches") + strings.Repeat("\n", h-1))
	}
	for i := m.offset; i < len(m.rows) && i < m.offset+h; i++ {
		b.WriteString(m.renderRow(i) + "\n")
	}
	for i := len(m.rows) - m.offset; i < h; i++ {
		b.WriteString("\n")
	}
	b.WriteString(m.renderDetail())
	if m.showHelp {
		return m.viewHelp()
	}
	return b.String()
}

func (m model) renderRow(i int) string {
	r := m.rows[i]
	var line string
	if r.header {
		fold := "▾"
		if m.collapsed[r.group] {
			fold = "▸"
		}
		line = headerStyle.Render(fmt.Sprintf("%s %s", fold, r.group)) + dimStyle.Render(fmt.Sprintf("  %d items, %s", r.count, report.HumanSize(r.bytes)))
	} else {
		c := r.cand
		box := "[ ]"
		if m.sel[c.Path] {
			box = "[x]"
		}
		if !selectable(c) {
			box = " - "
		}
		mark := report.Mark(c.Verdict)
		switch c.Verdict {
		case "safe":
			mark = safeStyle.Render(mark)
		case "review":
			mark = reviewStyle.Render(mark)
		}
		path := c.DisplayPath
		if c.NestedIn != "" {
			path += dimStyle.Render(" (nested)")
		}
		if c.Blocked != "" {
			path += warnStyle.Render(" (running)")
		}
		line = fmt.Sprintf("  %s %9s %s %s", box, report.HumanSize(c.Bytes), mark, path)
	}
	if i == m.cursor {
		return cursorStyle.Render(padRight(stripANSIWidth(line), m.width))
	}
	return line
}

func (m model) renderDetail() string {
	r := m.current()
	lines := make([]string, 0, detailLines)
	lines = append(lines, dimStyle.Render(strings.Repeat("─", max(m.width, 20))))
	if r == nil || r.header {
		if r != nil {
			lines = append(lines, fmt.Sprintf("group %s: space or a marks every safe/review item in it", r.group))
		}
	} else {
		c := r.cand
		lines = append(lines, fmt.Sprintf("%s  %s  rule %s", report.Mark(c.Verdict), c.Verdict, c.RuleID))
		lines = append(lines, "note:     "+c.Note)
		lines = append(lines, "recovery: "+c.Recovery)
		reasons := c.Reasons
		if len(reasons) > 1 {
			reasons = reasons[1:]
		}
		lines = append(lines, "why:      "+strings.Join(reasons, "; "))
		if t := c.Tags; len(t) > 0 {
			var facts []string
			for _, k := range []string{"downloaded", "source", "age_bucket", "last_used", "owner", "command"} {
				if v := t[k]; v != "" {
					facts = append(facts, k+": "+v)
				}
			}
			if len(facts) > 0 {
				lines = append(lines, "facts:    "+strings.Join(facts, "  "))
			}
		}
		if c.Blocked != "" {
			lines = append(lines, warnStyle.Render("blocked:  "+c.Blocked))
		}
	}
	for len(lines) < detailLines {
		lines = append(lines, "")
	}
	return strings.Join(lines[:detailLines], "\n")
}

func (m model) viewHelp() string {
	return titleStyle.Render("Keys") + `

  ↑/k ↓/j        move          PgUp/PgDn g/G  jump
  space          mark item     a              mark/unmark whole group
  tab / enter    fold group    /              filter (esc clears)
  e              explain why this path got its verdict
  d              review and trash the marked items (asks twice for ? items)
  q              quit without changes

  ✓ safe: the app recreates it.  ? review: your data, you decide.  - keep: never trashed.
  Items marked (running) belong to a running application and are skipped.

` + dimStyle.Render("press any key to return")
}

func (m model) viewExplain() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Explain  "+report.DisplayPath(m.explainPath, m.deps.Env.Home)) + "\n\n")
	switch {
	case m.explainErr != nil:
		b.WriteString(warnStyle.Render(m.explainErr.Error()))
	case m.explanation == nil:
		b.WriteString(m.spin.View() + " measuring…")
	default:
		report.WriteExplanation(&b, m.explanation, m.deps.Env.Home)
	}
	b.WriteString("\n\n" + dimStyle.Render("esc returns"))
	return b.String()
}

func (m model) viewConfirm() string {
	s := m.sel.summarize(m.rep)
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Move %d item(s), %s, to the Trash?", s.count, report.HumanSize(s.bytes))) + "\n\n")
	for _, g := range m.rep.Groups {
		if v := s.byGroup[g.Name]; v > 0 {
			b.WriteString(fmt.Sprintf("  %-28s %10s\n", g.Name, report.HumanSize(v)))
		}
	}
	if len(s.blockedApps) > 0 {
		b.WriteString("\n" + warnStyle.Render("Running, their data is skipped: "+strings.Join(s.blockedApps, ", ")) + "\n")
	}
	if s.review > 0 {
		b.WriteString("\n" + reviewStyle.Render(fmt.Sprintf("%d marked item(s) are ? review — meaningful data, not caches:", s.review)) + "\n")
		for i, c := range s.reviewItems {
			if i >= 8 {
				b.WriteString(dimStyle.Render(fmt.Sprintf("  … and %d more\n", len(s.reviewItems)-8)))
				break
			}
			b.WriteString(fmt.Sprintf("  ? %9s  %s\n", report.HumanSize(c.Bytes), c.DisplayPath))
		}
	}
	b.WriteString("\n" + dimStyle.Render("Everything goes to the Finder Trash; Put Back restores it. Each item is re-checked right before moving.") + "\n\n")
	if m.confirmStage == 1 {
		b.WriteString(warnStyle.Render("Press y again to confirm the ? review items too, or n to go back."))
	} else {
		b.WriteString("y to proceed, n to go back")
	}
	return b.String()
}

func (m model) viewDone() string {
	var b strings.Builder
	j := m.journal
	b.WriteString(titleStyle.Render(fmt.Sprintf("Moved to Trash: %s in %d item(s); skipped %d, failed %d", report.HumanSize(j.TrashedBytes), j.Trashed, j.Skipped, j.Failed)) + "\n\n")
	shown := 0
	for _, o := range j.Items {
		if o.Status == "contained" {
			continue
		}
		mark := safeStyle.Render("✓")
		switch o.Status {
		case "skipped":
			mark = reviewStyle.Render("–")
		case "failed":
			mark = warnStyle.Render("✗")
		}
		line := fmt.Sprintf("  %s %9s  %s", mark, report.HumanSize(o.Bytes), report.DisplayPath(o.Path, m.deps.Env.Home))
		if o.Reason != "" {
			line += dimStyle.Render("  " + o.Reason)
		}
		b.WriteString(line + "\n")
		shown++
		if shown >= m.height-8 && m.height > 0 {
			b.WriteString(dimStyle.Render("  …\n"))
			break
		}
	}
	b.WriteString("\n" + dimStyle.Render("Space is reclaimed when you empty the Trash. Journal written to ~/Library/Caches/macsweep/runs/. q quits."))
	return b.String()
}

func fmtInt(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}

func padRight(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// stripANSIWidth keeps the text but drops styling so the cursor highlight is uniform.
func stripANSIWidth(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}
