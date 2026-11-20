package lib

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

var (
	unoPort   = Port{Address: "/dev/cu.usbmodem1101", Kind: PortArduino, Boards: []Board{{"Arduino UNO", "arduino:avr:uno"}}}
	clonePort = Port{Address: "/dev/cu.usbserial-110", Kind: PortUSBSerial, VID: "0x1a86"}
	btPort    = Port{Address: "/dev/cu.Bluetooth-Incoming-Port", Kind: PortOther, ProtocolLabel: "Serial Port"}
	allBoards = []Board{{"Arduino Mega", "arduino:avr:mega"}, {"Arduino Nano", "arduino:avr:nano"}, {"Arduino UNO", "arduino:avr:uno"}}
)

// newTestLoadModel returns a model over a temp project with two sketches and
// stubbed port/board discovery.
func newTestLoadModel(t *testing.T, opts LoadOptions) *LoadModel {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "alpha.ino", "")
	writeFile(t, root, "beta.ino", "")
	opts.ProjectDir = root
	if opts.LibDir == "" {
		opts.LibDir = "lib"
	}
	m, err := NewLoadModel(&ArduinoCLI{Path: "/nonexistent/arduino-cli"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	m.ScanPorts = func() ([]Port, error) { return []Port{unoPort, clonePort, btPort}, nil }
	m.ListBoards = func() ([]Board, error) { return allBoards, nil }
	return m
}

// update is a convenience wrapper that type-asserts the returned tea.Model.
func update(m *LoadModel, msg tea.Msg) (*LoadModel, tea.Cmd) {
	model, cmd := m.Update(msg)
	return model.(*LoadModel), cmd
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// scanned feeds the stubbed scan result into the model.
func scanned(m *LoadModel) *LoadModel {
	ports, err := m.ScanPorts()
	m, _ = update(m, portsMsg{Ports: ports, Err: err})
	return m
}

// ──────────────────────────────────────────────────────────────────────────────
// Construction
// ──────────────────────────────────────────────────────────────────────────────

func TestNewLoadModel_StartsOnSketchPicker(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{})
	if m.Phase != PhaseSketch || len(m.Sketches) != 2 {
		t.Errorf("expected sketch phase with 2 sketches, got phase %d, %d sketches", m.Phase, len(m.Sketches))
	}
}

func TestNewLoadModel_NoSketchesErrors(t *testing.T) {
	if _, err := NewLoadModel(&ArduinoCLI{}, LoadOptions{ProjectDir: t.TempDir(), LibDir: "lib"}); err == nil {
		t.Error("expected an error when there are no .ino files")
	}
}

func TestNewLoadModel_PreselectedSketchSkipsPicker(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, root, "blink.ino", "")
	m, err := NewLoadModel(&ArduinoCLI{}, LoadOptions{ProjectDir: root, LibDir: "lib", Sketch: path})
	if err != nil {
		t.Fatal(err)
	}
	if m.Phase != PhasePort || m.Sketch == nil || m.Sketch.Name != "blink" {
		t.Errorf("expected port phase with blink selected, got phase %d", m.Phase)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Sketch → port flow
// ──────────────────────────────────────────────────────────────────────────────

func TestSketchPicker_SelectMovesToPortPicker(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{})
	m.Init()
	m, _ = update(m, key("down"))
	m, _ = update(m, key("down")) // clamped at the end
	m, _ = update(m, key("enter"))
	if m.Phase != PhasePort {
		t.Fatalf("expected port phase, got %d", m.Phase)
	}
	if m.Sketch.Name != "beta" {
		t.Errorf("expected beta selected, got %s", m.Sketch.Name)
	}
}

func TestPortPicker_EscGoesBackToSketches(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{})
	m, _ = update(m, key("enter"))
	m, _ = update(m, key("esc"))
	if m.Phase != PhaseSketch || m.Sketch != nil {
		t.Errorf("expected to be back on sketch picker with no selection")
	}
}

func TestPortScan_SchedulesRescanOnlyOnPortPhase(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{})
	m.Init()
	_, cmd := update(m, portsMsg{Ports: []Port{unoPort}})
	if cmd != nil {
		t.Error("expected no rescan while on the sketch picker")
	}
	m, _ = update(m, key("enter"))
	m.Scanning = true
	_, cmd = update(m, portsMsg{Ports: []Port{unoPort}})
	if cmd == nil {
		t.Error("expected a rescan to be scheduled on the port picker")
	}
}

func TestPortScan_CursorFollowsPortAcrossRescans(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{})
	m, _ = update(m, key("enter"))
	m = scanned(m)
	m, _ = update(m, key("down")) // clonePort
	m, _ = update(m, portsMsg{Ports: []Port{btPort, clonePort}})
	if m.Ports[m.PortCursor].Address != clonePort.Address {
		t.Errorf("expected cursor to stay on %s, got %s", clonePort.Address, m.Ports[m.PortCursor].Address)
	}
}

func TestPortScan_ErrorKeepsPreviousPorts(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{})
	m, _ = update(m, key("enter"))
	m = scanned(m)
	m, _ = update(m, portsMsg{Err: errors.New("boom")})
	if len(m.Ports) != 3 || m.ScanErr == nil {
		t.Errorf("expected previous ports kept and error recorded")
	}
	if !strings.Contains(m.View(), "scan failed: boom") {
		t.Error("expected scan error in view")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Board resolution
// ──────────────────────────────────────────────────────────────────────────────

func TestChoosePort_DetectedBoardStartsRun(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{})
	m, _ = update(m, key("enter"))
	m = scanned(m)
	m, cmd := update(m, key("enter"))
	if m.Phase != PhaseRun || m.FQBN != "arduino:avr:uno" || cmd == nil {
		t.Fatalf("expected run phase with arduino:avr:uno, got phase %d fqbn %q", m.Phase, m.FQBN)
	}
	if m.Stages[0].Status != StageRunning {
		t.Error("expected first stage to be running")
	}
	if m.Job.Port != unoPort.Address {
		t.Errorf("expected job port %s, got %s", unoPort.Address, m.Job.Port)
	}
}

func TestChoosePort_FQBNFlagOverridesDetection(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{FQBN: "arduino:avr:nano"})
	m, _ = update(m, key("enter"))
	m = scanned(m)
	m, _ = update(m, key("down"))
	m, _ = update(m, key("enter"))
	if m.Phase != PhaseRun || m.FQBN != "arduino:avr:nano" {
		t.Errorf("expected run with arduino:avr:nano, got phase %d fqbn %q", m.Phase, m.FQBN)
	}
}

func TestChoosePort_UnknownBoardShowsFilterablePicker(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{})
	m, _ = update(m, key("enter"))
	m = scanned(m)
	m, _ = update(m, key("down"))
	m, cmd := update(m, key("enter"))
	if m.Phase != PhaseBoard || !m.BoardsLoading || cmd == nil {
		t.Fatalf("expected board phase loading boards")
	}
	m, _ = update(m, cmd())
	if len(m.FilteredBoards()) != 3 {
		t.Fatalf("expected 3 boards, got %d", len(m.FilteredBoards()))
	}

	for _, r := range "nan" {
		m, _ = update(m, key(string(r)))
	}
	if got := m.FilteredBoards(); len(got) != 1 || got[0].FQBN != "arduino:avr:nano" {
		t.Fatalf("expected filter to match only Nano, got %+v", got)
	}
	m, _ = update(m, key("backspace"))
	if m.BoardFilter != "na" {
		t.Errorf("expected filter 'na' after backspace, got %q", m.BoardFilter)
	}
	m, _ = update(m, key("enter"))
	if m.Phase != PhaseRun || m.FQBN != "arduino:avr:nano" {
		t.Errorf("expected run with nano, got phase %d fqbn %q", m.Phase, m.FQBN)
	}
}

func TestBoardPicker_EscClearsFilterThenGoesBack(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{})
	m, _ = update(m, key("enter"))
	m = scanned(m)
	m, _ = update(m, key("down"))
	m, _ = update(m, key("enter"))
	m, _ = update(m, key("x"))
	m, _ = update(m, key("esc"))
	if m.Phase != PhaseBoard || m.BoardFilter != "" {
		t.Fatalf("expected first esc to clear the filter")
	}
	m, _ = update(m, key("esc"))
	if m.Phase != PhasePort || m.Port != nil {
		t.Errorf("expected second esc to return to port picker")
	}
}

func TestChoosePort_MultipleMatchesOffersThoseBoards(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{})
	multi := Port{Address: "/dev/ttyACM0", Kind: PortArduino, Boards: []Board{{"A", "x:y:a"}, {"B", "x:y:b"}}}
	m.ScanPorts = func() ([]Port, error) { return []Port{multi}, nil }
	m, _ = update(m, key("enter"))
	m = scanned(m)
	m, cmd := update(m, key("enter"))
	if m.Phase != PhaseBoard || m.BoardsLoading || cmd != nil || len(m.Boards) != 2 {
		t.Errorf("expected picker over the 2 matching boards without loading")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// --port preselection
// ──────────────────────────────────────────────────────────────────────────────

func TestPresetPort_SelectedAfterScan(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{Port: unoPort.Address})
	m, _ = update(m, key("enter"))
	m = scanned(m)
	if m.Phase != PhaseRun || m.Port.Address != unoPort.Address {
		t.Errorf("expected run on preset port, got phase %d", m.Phase)
	}
}

func TestPresetPort_ScanFinishedBeforeSketchPicked(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{Port: unoPort.Address})
	m = scanned(m) // scan completes while still on the sketch picker
	m, _ = update(m, key("enter"))
	if m.Phase != PhaseRun {
		t.Errorf("expected run on preset port, got phase %d", m.Phase)
	}
}

func TestPresetPort_NotFoundFallsBackToPicker(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{Port: "/dev/missing"})
	m, _ = update(m, key("enter"))
	m = scanned(m)
	if m.Phase != PhasePort || !strings.Contains(m.View(), "/dev/missing not found") {
		t.Errorf("expected port picker with not-found notice")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Stage pipeline
// ──────────────────────────────────────────────────────────────────────────────

func startedModel(t *testing.T) *LoadModel {
	m := newTestLoadModel(t, LoadOptions{})
	m, _ = update(m, key("enter"))
	m = scanned(m)
	m, _ = update(m, key("enter"))
	return m
}

func TestStageDone_AdvancesThenQuits(t *testing.T) {
	m := startedModel(t)
	for i := 0; i < len(m.Stages); i++ {
		var cmd tea.Cmd
		m, cmd = update(m, stageDoneMsg{Index: i, Detail: "ok"})
		if cmd == nil {
			t.Fatalf("expected a command after stage %d", i)
		}
	}
	if m.Phase != PhaseDone || m.Failed() {
		t.Fatalf("expected successful completion")
	}
	for _, s := range m.Stages {
		if s.Status != StageSuccess {
			t.Errorf("stage %s: expected success, got %s", s.Name, s.Status)
		}
	}
	if !strings.Contains(m.View(), "alpha uploaded to /dev/cu.usbmodem1101") {
		t.Errorf("expected success line in view:\n%s", m.View())
	}
}

func TestStageDone_FailureStopsPipeline(t *testing.T) {
	m := startedModel(t)
	m, _ = update(m, stageDoneMsg{Index: 0, Detail: "ok"})
	m, cmd := update(m, stageDoneMsg{Index: 1, Err: errors.New("core broke")})
	if cmd == nil || m.Phase != PhaseDone || !m.Failed() {
		t.Fatalf("expected failure to end the flow")
	}
	if m.Stages[1].Status != StageFailed || m.Stages[2].Status != StagePending {
		t.Errorf("unexpected statuses %s, %s", m.Stages[1].Status, m.Stages[2].Status)
	}
	if !strings.Contains(m.View(), "core failed") {
		t.Errorf("expected failure line in view")
	}
}

func TestCtrlC_CancelsRunningJob(t *testing.T) {
	m := startedModel(t)
	m, cmd := update(m, key("ctrl+c"))
	if cmd == nil || !m.Cancelled || !m.Failed() {
		t.Fatal("expected ctrl+c to cancel")
	}
	if m.Job.Ctx.Err() == nil {
		t.Error("expected job context to be cancelled")
	}
	// A stage finishing after cancellation must not restart the pipeline.
	if _, cmd := update(m, stageDoneMsg{Index: 0}); cmd != nil {
		t.Error("expected late stage result to be ignored")
	}
}

func TestJob_UsesProjectWorkDir(t *testing.T) {
	m := startedModel(t)
	if m.Job.WorkDir != filepath.Join(m.Opts.ProjectDir, WorkDirName) {
		t.Errorf("unexpected work dir %s", m.Job.WorkDir)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// View helpers
// ──────────────────────────────────────────────────────────────────────────────

func TestListWindow(t *testing.T) {
	cases := []struct{ cursor, total, rows, start, end int }{
		{0, 3, 10, 0, 3},
		{0, 20, 5, 0, 5},
		{10, 20, 5, 8, 13},
		{19, 20, 5, 15, 20},
	}
	for _, c := range cases {
		s, e := listWindow(c.cursor, c.total, c.rows)
		if s != c.start || e != c.end {
			t.Errorf("listWindow(%d,%d,%d) = %d,%d want %d,%d", c.cursor, c.total, c.rows, s, e, c.start, c.end)
		}
	}
}

func TestCompileSummary(t *testing.T) {
	out := "Sketch uses 924 bytes (2%) of program storage space.\nGlobal variables use 9 bytes (0%) of dynamic memory.\n"
	if got := compileSummary(out); got != "flash 924 B (2%) · ram 9 B (0%)" {
		t.Errorf("got %q", got)
	}
	if got := compileSummary("other output"); got != "compiled" {
		t.Errorf("got %q", got)
	}
}

func TestPortPickerView_HighlightsKinds(t *testing.T) {
	m := newTestLoadModel(t, LoadOptions{})
	m, _ = update(m, key("enter"))
	m = scanned(m)
	v := m.View()
	for _, want := range []string{"● /dev/cu.usbmodem1101", "Arduino UNO (arduino:avr:uno)", "◐ /dev/cu.usbserial-110", "○ /dev/cu.Bluetooth"} {
		if !strings.Contains(v, want) {
			t.Errorf("expected %q in view:\n%s", want, v)
		}
	}
}

func TestFormatPorts_IconsAndLegend(t *testing.T) {
	out := FormatPorts([]Port{unoPort, clonePort, btPort})
	for _, want := range []string{"● /dev/cu.usbmodem1101", "◐ /dev/cu.usbserial-110", "○ /dev/cu.Bluetooth", "usb serial (clone?)"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
}
