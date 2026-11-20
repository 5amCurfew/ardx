package lib

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Phase is the step of the upload flow the TUI is on.
type Phase int

const (
	PhaseSketch Phase = iota // choosing the .ino file
	PhasePort                // choosing the Arduino (port)
	PhaseBoard               // choosing the board type when the port didn't identify it
	PhaseRun                 // compiling and uploading
	PhaseDone                // finished (success, failure or cancelled)
)

// rescanInterval is how often the port list refreshes while it is on screen,
// so boards plugged in after launch appear without pressing a key.
const rescanInterval = 2 * time.Second

// tickMsg is sent on a timer to drive the spinner animation.
type tickMsg struct{}

// portsMsg carries the result of a port scan.
type portsMsg struct {
	Ports []Port
	Err   error
}

// rescanMsg asks the model to scan the ports again.
type rescanMsg struct{}

// boardsMsg carries the list of installed boards.
type boardsMsg struct {
	Boards []Board
	Err    error
}

// stageDoneMsg reports that a pipeline stage finished.
type stageDoneMsg struct {
	Index  int
	Detail string
	Err    error
}

// spinnerFrames is the braille-dot animation sequence used for running states.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))  // blue
	cursorStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))  // white
	arduinoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("34"))             // green
	cloneStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))            // yellow
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))            // grey
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("160"))            // red
	successStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("34"))  // green
	failStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("160")) // red
)

// stageStyles maps each StageStatus to the colour of its row.
var stageStyles = map[StageStatus]lipgloss.Style{
	StagePending: lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	StageRunning: lipgloss.NewStyle().Foreground(lipgloss.Color("15")),
	StageSuccess: lipgloss.NewStyle().Foreground(lipgloss.Color("34")),
	StageSkipped: lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	StageFailed:  lipgloss.NewStyle().Foreground(lipgloss.Color("160")),
}

// portIcons marks each port by how likely it is to be an Arduino.
var portIcons = map[PortKind]string{PortArduino: "●", PortUSBSerial: "◐", PortOther: "○"}

// portStyle returns the colour for a port of the given kind: green for
// Arduinos, yellow for USB-serial clones, grey for everything else.
func portStyle(kind PortKind) lipgloss.Style {
	switch kind {
	case PortArduino:
		return arduinoStyle
	case PortUSBSerial:
		return cloneStyle
	default:
		return dimStyle
	}
}

// PortLegend explains the port icons and colours.
func PortLegend() string {
	return fmt.Sprintf("%s  %s  %s",
		arduinoStyle.Render("● arduino"), cloneStyle.Render("◐ usb serial (clone?)"), dimStyle.Render("○ other"))
}

// FormatPorts renders ports as a coloured table with icons, as used by
// `ardx ports`. Colours are dropped automatically when output isn't a terminal.
func FormatPorts(ports []Port) string {
	width := len("Port")
	for _, p := range ports {
		width = max(width, runewidth.StringWidth(p.Address))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  %s  %s\n", runewidth.FillRight("Port", width), "Detected")
	fmt.Fprintf(&b, "%s\n", strings.Repeat("-", width+44))
	for _, p := range ports {
		row := fmt.Sprintf("%s %s  %s", portIcons[p.Kind], runewidth.FillRight(p.Address, width), p.Description())
		fmt.Fprintf(&b, "%s\n", portStyle(p.Kind).Render(row))
	}
	fmt.Fprintf(&b, "\n%s\n", PortLegend())
	return b.String()
}

// LoadOptions configures the upload flow. Any field left empty is
// chosen interactively.
type LoadOptions struct {
	ProjectDir string // directory to find sketches in and keep .ardx in
	LibDir     string // directory of library .zip files
	Sketch     string // preselected sketch (.ino path or sketch folder)
	Port       string // preselected port address
	FQBN       string // board FQBN, overriding auto-detection
}

// LoadModel is the Bubble Tea model for `ardx upload`: pick a sketch,
// pick an Arduino, then compile and upload with a live stage table.
type LoadModel struct {
	Opts  LoadOptions
	CLI   *ArduinoCLI
	Phase Phase

	ScanPorts  func() ([]Port, error)  // swappable for tests
	ListBoards func() ([]Board, error) // swappable for tests

	Sketches     []Sketch
	SketchCursor int
	Sketch       *Sketch

	Ports      []Port
	PortCursor int
	Scanning   bool
	Scanned    bool
	ScanErr    error
	PortNotice string // e.g. a --port that wasn't found
	Port       *Port

	Boards        []Board
	BoardFilter   string
	BoardCursor   int
	BoardsLoading bool
	BoardErr      error
	FQBN          string
	BoardName     string

	Job     *Job
	Stages  []*Stage
	Current int

	Err          error // fatal error ending the flow
	Cancelled    bool
	SpinnerFrame int
	Height       int
}

// NewLoadModel builds the model, discovering sketches in opts.ProjectDir
// unless one was preselected.
func NewLoadModel(cli *ArduinoCLI, opts LoadOptions) (*LoadModel, error) {
	m := &LoadModel{
		Opts:       opts,
		CLI:        cli,
		ScanPorts:  cli.ListPorts,
		ListBoards: cli.ListBoards,
		FQBN:       opts.FQBN,
	}
	if opts.Sketch != "" {
		s, err := NewSketch(opts.Sketch)
		if err != nil {
			return nil, err
		}
		m.Sketch = &s
		m.Phase = PhasePort
		return m, nil
	}
	sketches, err := FindSketches(opts.ProjectDir, opts.LibDir)
	if err != nil {
		return nil, err
	}
	if len(sketches) == 0 {
		return nil, fmt.Errorf("no .ino files found in %s", opts.ProjectDir)
	}
	m.Sketches = sketches
	return m, nil
}

// Failed reports whether the flow ended without a successful upload.
func (m *LoadModel) Failed() bool {
	return m.Err != nil || m.Cancelled
}

// Init starts the spinner and kicks off a port scan straight away so the
// list is ready by the time a sketch has been picked.
func (m *LoadModel) Init() tea.Cmd {
	m.Scanning = true
	return tea.Batch(tick(), m.scanCmd())
}

func tick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *LoadModel) scanCmd() tea.Cmd {
	scan := m.ScanPorts
	return func() tea.Msg {
		ports, err := scan()
		return portsMsg{Ports: ports, Err: err}
	}
}

func (m *LoadModel) boardsCmd() tea.Cmd {
	list := m.ListBoards
	return func() tea.Msg {
		boards, err := list()
		return boardsMsg{Boards: boards, Err: err}
	}
}

func runStageCmd(job *Job, stage *Stage, index int) tea.Cmd {
	return func() tea.Msg {
		detail, err := stage.Run(job)
		return stageDoneMsg{Index: index, Detail: detail, Err: err}
	}
}

// Update handles key presses, scan/list results and stage completions.
func (m *LoadModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Height = msg.Height
	case tickMsg:
		if m.Phase == PhaseDone {
			return m, nil
		}
		m.SpinnerFrame = (m.SpinnerFrame + 1) % len(spinnerFrames)
		return m, tick()
	case portsMsg:
		return m, m.handlePorts(msg)
	case rescanMsg:
		if m.Phase == PhasePort && !m.Scanning {
			m.Scanning = true
			return m, m.scanCmd()
		}
	case boardsMsg:
		m.BoardsLoading = false
		m.BoardErr = msg.Err
		m.Boards = msg.Boards
		m.BoardCursor = 0
	case stageDoneMsg:
		return m, m.handleStageDone(msg)
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, m.cancel()
		}
		switch m.Phase {
		case PhaseSketch:
			return m, m.updateSketch(msg)
		case PhasePort:
			return m, m.updatePort(msg)
		case PhaseBoard:
			return m, m.updateBoard(msg)
		}
	}
	return m, nil
}

// cancel aborts the flow, killing any running arduino-cli process.
func (m *LoadModel) cancel() tea.Cmd {
	if m.Job != nil {
		m.Job.Cancel()
	}
	m.Cancelled = true
	m.Phase = PhaseDone
	return tea.Quit
}

func (m *LoadModel) updateSketch(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		m.SketchCursor = max(0, m.SketchCursor-1)
	case "down", "j":
		m.SketchCursor = min(len(m.Sketches)-1, m.SketchCursor+1)
	case "enter":
		s := m.Sketches[m.SketchCursor]
		m.Sketch = &s
		return m.enterPortPhase()
	case "q", "esc":
		return m.cancel()
	}
	return nil
}

// enterPortPhase moves to the port picker, rescanning unless a scan is
// already running. If the scan already finished and a --port was given, it
// is selected immediately.
func (m *LoadModel) enterPortPhase() tea.Cmd {
	m.Phase = PhasePort
	if m.Scanned && m.Opts.Port != "" {
		if cmd, ok := m.selectPresetPort(); ok {
			return cmd
		}
	}
	if m.Scanning {
		return nil
	}
	m.Scanning = true
	return m.scanCmd()
}

func (m *LoadModel) handlePorts(msg portsMsg) tea.Cmd {
	m.Scanning = false
	m.ScanErr = msg.Err
	if msg.Err == nil {
		// Keep the cursor on the same port across rescans.
		var selected string
		if m.Scanned && m.PortCursor < len(m.Ports) {
			selected = m.Ports[m.PortCursor].Address
		}
		m.Ports = msg.Ports
		m.PortCursor = 0
		for i, p := range m.Ports {
			if p.Address == selected {
				m.PortCursor = i
			}
		}
	}
	firstScan := !m.Scanned
	m.Scanned = true

	if m.Phase != PhasePort {
		return nil
	}
	if firstScan && m.Opts.Port != "" {
		if cmd, ok := m.selectPresetPort(); ok {
			return cmd
		}
	}
	return tea.Tick(rescanInterval, func(time.Time) tea.Msg { return rescanMsg{} })
}

// selectPresetPort selects the --port given on the command line if it was
// found by the scan; otherwise it leaves a notice and the picker open.
func (m *LoadModel) selectPresetPort() (tea.Cmd, bool) {
	preset := m.Opts.Port
	m.Opts.Port = "" // only try once
	for i, p := range m.Ports {
		if p.Address == preset {
			m.PortCursor = i
			return m.choosePort(p), true
		}
	}
	m.PortNotice = fmt.Sprintf("port %s not found, pick one below", preset)
	return nil, false
}

func (m *LoadModel) updatePort(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		m.PortCursor = max(0, m.PortCursor-1)
	case "down", "j":
		m.PortCursor = max(0, min(len(m.Ports)-1, m.PortCursor+1))
	case "r":
		if !m.Scanning {
			m.Scanning = true
			return m.scanCmd()
		}
	case "enter":
		if len(m.Ports) == 0 {
			return nil
		}
		return m.choosePort(m.Ports[m.PortCursor])
	case "esc", "backspace":
		if len(m.Sketches) > 0 {
			m.Phase = PhaseSketch
			m.Sketch = nil
		}
	case "q":
		return m.cancel()
	}
	return nil
}

// choosePort records the port and works out the board: --fqbn wins, then a
// single detected board, otherwise the board picker is shown.
func (m *LoadModel) choosePort(p Port) tea.Cmd {
	m.Port = &p
	m.PortNotice = ""
	switch {
	case m.Opts.FQBN != "":
		m.FQBN = m.Opts.FQBN
		return m.startRun()
	case p.FQBN() != "":
		m.FQBN = p.FQBN()
		m.BoardName = p.Boards[0].Name
		return m.startRun()
	}
	m.Phase = PhaseBoard
	m.BoardFilter = ""
	m.BoardCursor = 0
	m.BoardErr = nil
	if len(p.Boards) > 1 {
		m.Boards = p.Boards
		return nil
	}
	m.Boards = nil
	m.BoardsLoading = true
	return m.boardsCmd()
}

// FilteredBoards returns the boards matching the typed filter.
func (m *LoadModel) FilteredBoards() []Board {
	if m.BoardFilter == "" {
		return m.Boards
	}
	f := strings.ToLower(m.BoardFilter)
	var out []Board
	for _, b := range m.Boards {
		if strings.Contains(strings.ToLower(b.Name), f) || strings.Contains(strings.ToLower(b.FQBN), f) {
			out = append(out, b)
		}
	}
	return out
}

func (m *LoadModel) updateBoard(msg tea.KeyMsg) tea.Cmd {
	boards := m.FilteredBoards()
	switch msg.Type {
	case tea.KeyUp:
		m.BoardCursor = max(0, m.BoardCursor-1)
	case tea.KeyDown:
		m.BoardCursor = max(0, min(len(boards)-1, m.BoardCursor+1))
	case tea.KeyEnter:
		if len(boards) == 0 {
			return nil
		}
		b := boards[m.BoardCursor]
		m.FQBN = b.FQBN
		m.BoardName = b.Name
		return m.startRun()
	case tea.KeyBackspace:
		if m.BoardFilter != "" {
			r := []rune(m.BoardFilter)
			m.BoardFilter = string(r[:len(r)-1])
			m.BoardCursor = 0
		}
	case tea.KeyEsc:
		if m.BoardFilter != "" {
			m.BoardFilter = ""
			m.BoardCursor = 0
			return nil
		}
		m.Port = nil
		return m.enterPortPhase()
	case tea.KeyRunes, tea.KeySpace:
		m.BoardFilter += string(msg.Runes)
		m.BoardCursor = 0
	}
	return nil
}

// startRun creates the job and runs its first stage.
func (m *LoadModel) startRun() tea.Cmd {
	m.Phase = PhaseRun
	m.Job = NewJob(m.CLI, *m.Sketch, m.Port.Address, m.FQBN, m.Opts.LibDir, m.Opts.ProjectDir)
	m.Stages = m.Job.Stages()
	m.Current = 0
	return m.runStage(0)
}

func (m *LoadModel) runStage(i int) tea.Cmd {
	s := m.Stages[i]
	s.Status = StageRunning
	s.Start = time.Now()
	return runStageCmd(m.Job, s, i)
}

func (m *LoadModel) handleStageDone(msg stageDoneMsg) tea.Cmd {
	if m.Phase != PhaseRun || msg.Index >= len(m.Stages) {
		return nil
	}
	s := m.Stages[msg.Index]
	s.End = time.Now()
	s.Detail = msg.Detail
	if msg.Err != nil {
		s.Status = StageFailed
		s.Detail = msg.Err.Error()
		m.Err = msg.Err
		m.Phase = PhaseDone
		return tea.Quit
	}
	s.Status = StageSuccess
	if msg.Index+1 < len(m.Stages) {
		m.Current = msg.Index + 1
		return m.runStage(m.Current)
	}
	m.Phase = PhaseDone
	return tea.Quit
}

// ──────────────────────────────────────────────────────────────────────────────
// View
// ──────────────────────────────────────────────────────────────────────────────

// View renders the selections made so far followed by the current step.
func (m *LoadModel) View() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s %s\n\n", titleStyle.Render("ardx"), dimStyle.Render("upload"))
	m.viewSelections(&b)

	switch m.Phase {
	case PhaseSketch:
		m.viewSketchPicker(&b)
	case PhasePort:
		m.viewPortPicker(&b)
	case PhaseBoard:
		m.viewBoardPicker(&b)
	case PhaseRun, PhaseDone:
		m.viewStages(&b)
	}
	return b.String()
}

// viewSelections lists the choices already made, e.g. "✓ sketch  blink.ino".
func (m *LoadModel) viewSelections(b *strings.Builder) {
	if m.Sketch != nil {
		fmt.Fprintf(b, "%s %-8s %s\n", successStyle.Render("✓"), "sketch", m.Sketch.Rel(m.Opts.ProjectDir))
	}
	if m.Port != nil && m.Phase >= PhaseBoard {
		fmt.Fprintf(b, "%s %-8s %s\n", successStyle.Render("✓"), "arduino", m.Port.Address)
	}
	if m.FQBN != "" && m.Phase >= PhaseRun {
		board := m.FQBN
		if m.BoardName != "" {
			board = fmt.Sprintf("%s (%s)", m.BoardName, m.FQBN)
		}
		fmt.Fprintf(b, "%s %-8s %s\n", successStyle.Render("✓"), "board", board)
	}
	if m.Sketch != nil {
		b.WriteString("\n")
	}
}

func (m *LoadModel) viewSketchPicker(b *strings.Builder) {
	fmt.Fprintf(b, "%s %s\n\n", titleStyle.Render("Pick a sketch"), dimStyle.Render("in "+m.Opts.ProjectDir))
	start, end := listWindow(m.SketchCursor, len(m.Sketches), m.maxRows())
	for i := start; i < end; i++ {
		name := m.Sketches[i].Rel(m.Opts.ProjectDir)
		if i == m.SketchCursor {
			fmt.Fprintf(b, "%s\n", cursorStyle.Render("▸ "+name))
		} else {
			fmt.Fprintf(b, "  %s\n", name)
		}
	}
	m.viewScrollHint(b, start, end, len(m.Sketches))
	fmt.Fprintf(b, "\n%s\n", dimStyle.Render("↑/↓ move · enter select · q quit"))
}

func (m *LoadModel) viewPortPicker(b *strings.Builder) {
	status := ""
	if m.Scanning {
		status = spinnerFrames[m.SpinnerFrame] + " scanning"
	} else if m.Scanned {
		status = fmt.Sprintf("%d port(s), auto-refreshing", len(m.Ports))
	}
	fmt.Fprintf(b, "%s %s\n\n", titleStyle.Render("Pick an Arduino"), dimStyle.Render(status))
	if m.PortNotice != "" {
		fmt.Fprintf(b, "%s\n\n", cloneStyle.Render(m.PortNotice))
	}
	if m.ScanErr != nil {
		fmt.Fprintf(b, "%s\n\n", errorStyle.Render("scan failed: "+m.ScanErr.Error()))
	}
	if m.Scanned && len(m.Ports) == 0 {
		fmt.Fprintf(b, "  %s\n", dimStyle.Render("no ports found, plug in an Arduino"))
	}

	width := len("Port")
	for _, p := range m.Ports {
		width = max(width, runewidth.StringWidth(p.Address))
	}
	start, end := listWindow(m.PortCursor, len(m.Ports), m.maxRows())
	for i := start; i < end; i++ {
		p := m.Ports[i]
		row := fmt.Sprintf("%s %s  %s", portIcons[p.Kind], runewidth.FillRight(p.Address, width), p.Description())
		style := portStyle(p.Kind)
		if i == m.PortCursor {
			fmt.Fprintf(b, "%s%s\n", cursorStyle.Render("▸ "), style.Bold(true).Render(row))
		} else {
			fmt.Fprintf(b, "  %s\n", style.Render(row))
		}
	}
	m.viewScrollHint(b, start, end, len(m.Ports))
	fmt.Fprintf(b, "\n%s\n", PortLegend())
	back := ""
	if len(m.Sketches) > 0 {
		back = " · esc back"
	}
	fmt.Fprintf(b, "%s\n", dimStyle.Render("↑/↓ move · enter select · r rescan"+back+" · q quit"))
}

func (m *LoadModel) viewBoardPicker(b *strings.Builder) {
	reason := "board on this port couldn't be identified"
	if m.Port != nil && len(m.Port.Boards) > 1 {
		reason = "several boards match this port"
	}
	fmt.Fprintf(b, "%s %s\n\n", titleStyle.Render("Pick the board type"), dimStyle.Render(reason))
	fmt.Fprintf(b, "filter: %s%s\n\n", m.BoardFilter, cursorStyle.Render("█"))

	if m.BoardsLoading {
		fmt.Fprintf(b, "  %s loading installed boards\n", spinnerFrames[m.SpinnerFrame])
	} else if m.BoardErr != nil {
		fmt.Fprintf(b, "%s\n", errorStyle.Render("listing boards failed: "+m.BoardErr.Error()))
	} else {
		boards := m.FilteredBoards()
		if len(boards) == 0 {
			fmt.Fprintf(b, "  %s\n", dimStyle.Render("no boards match"))
		}
		width := 0
		for _, bd := range boards {
			width = max(width, runewidth.StringWidth(bd.Name))
		}
		start, end := listWindow(m.BoardCursor, len(boards), m.maxRows()-2)
		for i := start; i < end; i++ {
			row := fmt.Sprintf("%s  %s", runewidth.FillRight(boards[i].Name, width), dimStyle.Render(boards[i].FQBN))
			if i == m.BoardCursor {
				fmt.Fprintf(b, "%s\n", cursorStyle.Render("▸ "+row))
			} else {
				fmt.Fprintf(b, "  %s\n", row)
			}
		}
		m.viewScrollHint(b, start, end, len(boards))
	}
	fmt.Fprintf(b, "\n%s\n", dimStyle.Render("type to filter · ↑/↓ move · enter select · esc back · ctrl+c quit"))
}

// viewStages renders the pipeline as a table, in the style of orca's run view.
func (m *LoadModel) viewStages(b *strings.Builder) {
	if len(m.Stages) == 0 {
		if m.Cancelled {
			fmt.Fprintf(b, "%s\n", dimStyle.Render("cancelled"))
		}
		return
	}
	fmt.Fprintf(b, "%-10s %-12s %-10s %s\n", "Stage", "Status", "Took", "Detail")
	fmt.Fprintf(b, "%s\n", strings.Repeat("-", 60))
	for _, s := range m.Stages {
		status := ""
		switch s.Status {
		case StagePending:
			status = "[ ] Pending"
		case StageRunning:
			status = fmt.Sprintf(" %s  Running", spinnerFrames[m.SpinnerFrame])
		case StageSuccess:
			status = "[✓] Done"
		case StageSkipped:
			status = "[-] Skipped"
		case StageFailed:
			status = "[X] Failed"
		}
		took := "-"
		if !s.Start.IsZero() {
			end := s.End
			if end.IsZero() {
				end = time.Now()
			}
			took = fmt.Sprintf("%.1fs", end.Sub(s.Start).Seconds())
		}
		fmt.Fprintf(b, "%s\n", stageStyles[s.Status].Render(fmt.Sprintf(
			"%-10s %s %-10s %s",
			s.Name, runewidth.FillRight(status, 12), took, s.Detail,
		)))
	}

	if m.Phase != PhaseDone {
		fmt.Fprintf(b, "\n%s\n", dimStyle.Render("ctrl+c cancel"))
		return
	}
	switch {
	case m.Cancelled:
		fmt.Fprintf(b, "\n%s\n", failStyle.Render("✗ cancelled"))
	case m.Err != nil:
		failed := m.Stages[m.Current]
		fmt.Fprintf(b, "\n%s\n", failStyle.Render("✗ "+failed.Name+" failed"))
		if tail := m.Job.LogTail(failed.Name, 15); len(tail) > 0 {
			fmt.Fprintf(b, "\n%s\n", strings.Join(tail, "\n"))
		}
		if logPath := m.Job.LogPath(failed.Name); fileExists(logPath) {
			fmt.Fprintf(b, "\n%s\n", dimStyle.Render("full log: "+relPath(m.Opts.ProjectDir, logPath)))
		}
	default:
		fmt.Fprintf(b, "\n%s\n", successStyle.Render(fmt.Sprintf("✓ %s uploaded to %s", m.Sketch.Name, m.Port.Address)))
	}
}

// viewScrollHint shows how many items are hidden above/below the window.
func (m *LoadModel) viewScrollHint(b *strings.Builder, start, end, total int) {
	if start > 0 || end < total {
		fmt.Fprintf(b, "%s\n", dimStyle.Render(fmt.Sprintf("  … %d-%d of %d", start+1, end, total)))
	}
}

// maxRows is how many list rows fit on screen alongside the chrome.
func (m *LoadModel) maxRows() int {
	if m.Height == 0 {
		return 15
	}
	return max(3, m.Height-14)
}

// listWindow returns the [start, end) slice of a list of total items that
// keeps the cursor visible within rows lines.
func listWindow(cursor, total, rows int) (int, int) {
	rows = max(1, rows)
	if total <= rows {
		return 0, total
	}
	start := cursor - rows/2
	start = max(0, min(start, total-rows))
	return start, start + rows
}

// fileExists reports whether path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// relPath returns target relative to base when possible.
func relPath(base, target string) string {
	if rel, err := filepath.Rel(base, target); err == nil {
		return rel
	}
	return target
}
