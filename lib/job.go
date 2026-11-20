package lib

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// WorkDirName is the per-project folder ardx keeps its build cache,
// extracted libraries, staged sketches and logs in.
const WorkDirName = ".ardx"

var (
	flashRe = regexp.MustCompile(`Sketch uses (\d+) bytes \((\d+)%\)`)
	ramRe   = regexp.MustCompile(`Global variables use (\d+) bytes \((\d+)%\)`)
)

// StageStatus represents the lifecycle state of a pipeline stage.
type StageStatus string

const (
	StagePending StageStatus = "pending" // not started yet
	StageRunning StageStatus = "running" // in progress
	StageSuccess StageStatus = "success" // finished successfully
	StageSkipped StageStatus = "skipped" // nothing to do
	StageFailed  StageStatus = "failed"  // finished with an error
)

// Stage is one step of loading a sketch.
type Stage struct {
	Name   string
	Run    func(j *Job) (string, error)
	Status StageStatus
	Detail string
	Start  time.Time
	End    time.Time
}

// Job holds everything needed to compile a sketch and upload it to a board.
type Job struct {
	CLI     *ArduinoCLI
	Sketch  Sketch
	Port    string
	FQBN    string
	LibDir  string    // folder of library .zip files, e.g. "lib"
	WorkDir string    // ardx working folder, e.g. ".ardx"
	Started time.Time // used to name the log folder
	Ctx     context.Context
	Cancel  context.CancelFunc

	sketchDir string    // folder handed to arduino-cli (possibly staged)
	libraries []Library // libraries extracted from LibDir
}

// NewJob creates a Job rooted at the given project directory.
func NewJob(cli *ArduinoCLI, sketch Sketch, port, fqbn, libDir, projectDir string) *Job {
	ctx, cancel := context.WithCancel(context.Background())
	return &Job{
		Ctx:     ctx,
		Cancel:  cancel,
		CLI:     cli,
		Sketch:  sketch,
		Port:    port,
		FQBN:    fqbn,
		LibDir:  libDir,
		WorkDir: filepath.Join(projectDir, WorkDirName),
		Started: time.Now(),
	}
}

// Stages returns the ordered pipeline for this job, all pending.
func (j *Job) Stages() []*Stage {
	return []*Stage{
		{Name: "libraries", Run: (*Job).prepareLibraries, Status: StagePending},
		{Name: "core", Run: (*Job).ensureCore, Status: StagePending},
		{Name: "compile", Run: (*Job).compile, Status: StagePending},
		{Name: "upload", Run: (*Job).upload, Status: StagePending},
	}
}

// LogDir is the folder this job's stage logs are written to.
func (j *Job) LogDir() string {
	return filepath.Join(j.WorkDir, "logs", j.Started.Format("2006-01-02_15-04-05"))
}

// LogPath is the log file for the named stage.
func (j *Job) LogPath(stage string) string {
	return filepath.Join(j.LogDir(), stage+".log")
}

// buildPath is the cached build folder for this sketch and board, so
// repeat uploads only recompile what changed.
func (j *Job) buildPath() string {
	fqbn := strings.NewReplacer(":", "_", ",", "_", "=", "_").Replace(j.FQBN)
	return filepath.Join(j.WorkDir, "build", j.Sketch.Name, fqbn)
}

// libraryDirs are the library collections passed to arduino-cli: the
// extracted zips plus LibDir itself if it holds unzipped library folders.
func (j *Job) libraryDirs() []string {
	var dirs []string
	if len(j.libraries) > 0 {
		dirs = append(dirs, filepath.Join(j.WorkDir, "libraries"))
	}
	if entries, err := os.ReadDir(j.LibDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				dirs = append(dirs, j.LibDir)
				break
			}
		}
	}
	return dirs
}

// prepareLibraries extracts the lib directory's zips and stages the sketch.
func (j *Job) prepareLibraries() (string, error) {
	libs, err := PrepareLibraries(j.LibDir, filepath.Join(j.WorkDir, "libraries"))
	if err != nil {
		return "", err
	}
	j.libraries = libs

	j.sketchDir, err = j.Sketch.Stage(filepath.Join(j.WorkDir, "sketches"))
	if err != nil {
		return "", fmt.Errorf("staging sketch: %w", err)
	}

	if len(libs) == 0 {
		return fmt.Sprintf("no .zip libraries in %s/", j.LibDir), nil
	}
	includes, err := j.Sketch.Includes()
	if err != nil {
		return "", err
	}
	used := UsedLibraries(libs, includes)
	if len(used) == 0 {
		return fmt.Sprintf("%d zip(s) extracted, none included by sketch", len(libs)), nil
	}
	names := make([]string, len(used))
	for i, l := range used {
		names[i] = l.Name
	}
	return fmt.Sprintf("%d zip(s) extracted, using %s", len(libs), strings.Join(names, ", ")), nil
}

// ensureCore installs the board's platform (e.g. arduino:avr) if missing.
func (j *Job) ensureCore() (string, error) {
	platform, err := platformID(j.FQBN)
	if err != nil {
		return "", err
	}
	installed, err := j.CLI.InstalledPlatforms()
	if err != nil {
		return "", err
	}
	if installed[platform] {
		return platform + " installed", nil
	}
	if err := j.runLogged("core", func(buf *bytes.Buffer) error {
		return j.CLI.InstallPlatform(j.Ctx, platform, buf)
	}); err != nil {
		return "", err
	}
	return "installed " + platform, nil
}

// compile builds the sketch, returning a flash/RAM usage summary.
func (j *Job) compile() (string, error) {
	var out string
	err := j.runLogged("compile", func(buf *bytes.Buffer) error {
		err := j.CLI.Compile(j.Ctx, j.sketchDir, j.FQBN, j.buildPath(), j.libraryDirs(), buf)
		out = buf.String()
		return err
	})
	if err != nil {
		return "", err
	}
	return compileSummary(out), nil
}

// upload flashes the compiled sketch onto the board.
func (j *Job) upload() (string, error) {
	if err := j.runLogged("upload", func(buf *bytes.Buffer) error {
		return j.CLI.Upload(j.Ctx, j.sketchDir, j.FQBN, j.buildPath(), j.Port, buf)
	}); err != nil {
		return "", err
	}
	return "flashed " + j.Port, nil
}

// runLogged runs fn with an output buffer and writes the buffer to the
// stage's log file. Paths to a staged sketch copy are rewritten to point at
// the original file so compiler errors are clickable in the editor.
func (j *Job) runLogged(stage string, fn func(buf *bytes.Buffer) error) error {
	var buf bytes.Buffer
	runErr := fn(&buf)
	out := j.rewritePaths(buf.String())
	if err := os.MkdirAll(j.LogDir(), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(j.LogPath(stage), []byte(out), 0o644); err != nil {
		return err
	}
	if runErr != nil {
		return fmt.Errorf("%s failed: %w", stage, runErr)
	}
	return nil
}

// rewritePaths replaces the staged sketch path with the original one.
func (j *Job) rewritePaths(s string) string {
	if j.sketchDir == "" || j.Sketch.IsStandalone() {
		return s
	}
	staged := filepath.Join(j.sketchDir, j.Sketch.Name+".ino")
	s = strings.ReplaceAll(s, staged, j.Sketch.Path)
	return strings.ReplaceAll(s, j.sketchDir+string(os.PathSeparator), j.Sketch.Dir+string(os.PathSeparator))
}

// compileSummary condenses arduino-cli's memory usage lines.
func compileSummary(out string) string {
	var parts []string
	if m := flashRe.FindStringSubmatch(out); m != nil {
		parts = append(parts, fmt.Sprintf("flash %s B (%s%%)", m[1], m[2]))
	}
	if m := ramRe.FindStringSubmatch(out); m != nil {
		parts = append(parts, fmt.Sprintf("ram %s B (%s%%)", m[1], m[2]))
	}
	if len(parts) == 0 {
		return "compiled"
	}
	return strings.Join(parts, " · ")
}

// LogTail returns the last n non-empty lines of a stage's log.
func (j *Job) LogTail(stage string, n int) []string {
	data, err := os.ReadFile(j.LogPath(stage))
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
