package lib

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ArduinoCLIEnv is the environment variable that, when set, overrides the
// location of the arduino-cli binary used by ardx.
const ArduinoCLIEnv = "ARDX_ARDUINO_CLI"

// bundledCLIPaths are the locations where the Arduino IDE 2.x ships its own
// copy of arduino-cli. ardx falls back to these when arduino-cli is not on
// the PATH, so a plain IDE install is enough to use ardx.
var bundledCLIPaths = []string{
	"/Applications/Arduino IDE.app/Contents/Resources/app/lib/backend/resources/arduino-cli",
	"~/Applications/Arduino IDE.app/Contents/Resources/app/lib/backend/resources/arduino-cli",
	"/opt/arduino-ide/resources/app/lib/backend/resources/arduino-cli",
	"/usr/share/arduino-ide/resources/app/lib/backend/resources/arduino-cli",
}

// ArduinoCLI wraps the arduino-cli binary, which does the actual board
// discovery, compilation and flashing (it is the same engine the Arduino IDE
// uses under the hood).
type ArduinoCLI struct {
	Path string
}

// FindArduinoCLI locates arduino-cli, checking in order: the ARDX_ARDUINO_CLI
// environment variable, the PATH, and the copy bundled with the Arduino IDE.
func FindArduinoCLI() (*ArduinoCLI, error) {
	if p := os.Getenv(ArduinoCLIEnv); p != "" {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("%s points to %s: %w", ArduinoCLIEnv, p, err)
		}
		return &ArduinoCLI{Path: p}, nil
	}
	if p, err := exec.LookPath("arduino-cli"); err == nil {
		return &ArduinoCLI{Path: p}, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range bundledCLIPaths {
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		}
		if _, err := os.Stat(p); err == nil {
			return &ArduinoCLI{Path: p}, nil
		}
	}
	return nil, fmt.Errorf("arduino-cli not found: install the Arduino IDE or arduino-cli (brew install arduino-cli), or set %s", ArduinoCLIEnv)
}

// output runs arduino-cli with the given args and returns stdout. On failure
// the error includes whatever arduino-cli printed to stderr.
func (a *ArduinoCLI) output(args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(a.Path, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("arduino-cli %s: %w: %s", args[0], err, msg)
	}
	return stdout.Bytes(), nil
}

// stream runs arduino-cli with the given args, writing stdout and stderr to
// w. The process is killed if ctx is cancelled.
func (a *ArduinoCLI) stream(ctx context.Context, w io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, a.Path, args...)
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}

// ListPorts runs `arduino-cli board list` and returns every detected port,
// Arduino-identified ports first.
func (a *ArduinoCLI) ListPorts() ([]Port, error) {
	out, err := a.output("board", "list", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parsePorts(out)
}

// ListBoards returns every board offered by the installed platforms
// (`arduino-cli board listall`), used when a port's board can't be identified.
func (a *ArduinoCLI) ListBoards() ([]Board, error) {
	out, err := a.output("board", "listall", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parseBoards(out)
}

// InstalledPlatforms returns the set of installed platform ids, e.g. "arduino:avr".
func (a *ArduinoCLI) InstalledPlatforms() (map[string]bool, error) {
	out, err := a.output("core", "list", "--format", "json")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Platforms []struct {
			ID string `json:"id"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("parsing core list: %w", err)
	}
	installed := make(map[string]bool)
	for _, p := range parsed.Platforms {
		installed[p.ID] = true
	}
	return installed, nil
}

// InstallPlatform refreshes the package index and installs the given
// platform (e.g. "arduino:avr"), streaming output to w.
func (a *ArduinoCLI) InstallPlatform(ctx context.Context, id string, w io.Writer) error {
	if err := a.stream(ctx, w, "core", "update-index", "--no-color"); err != nil {
		return fmt.Errorf("updating package index: %w", err)
	}
	if err := a.stream(ctx, w, "core", "install", id, "--no-color"); err != nil {
		return fmt.Errorf("installing %s: %w", id, err)
	}
	return nil
}

// Compile builds the sketch folder for fqbn into buildPath, adding each of
// libraryDirs as an extra collection of libraries.
func (a *ArduinoCLI) Compile(ctx context.Context, sketchDir, fqbn, buildPath string, libraryDirs []string, w io.Writer) error {
	args := []string{"compile", "--no-color", "--fqbn", fqbn, "--build-path", buildPath}
	for _, d := range libraryDirs {
		args = append(args, "--libraries", d)
	}
	args = append(args, sketchDir)
	return a.stream(ctx, w, args...)
}

// Upload flashes the binary previously compiled into buildPath onto the
// board at port.
func (a *ArduinoCLI) Upload(ctx context.Context, sketchDir, fqbn, buildPath, port string, w io.Writer) error {
	return a.stream(ctx, w, "upload", "--no-color", "--fqbn", fqbn, "--port", port, "--input-dir", buildPath, sketchDir)
}
