package lib

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ──────────────────────────────────────────────────────────────────────────────
// Integration: real arduino-cli compile (no board needed). Opt in with
// ARDX_INTEGRATION=1 since it needs arduino-cli and the arduino:avr core.
// ──────────────────────────────────────────────────────────────────────────────

func TestIntegration_CompileWithZipLibrary(t *testing.T) {
	if os.Getenv("ARDX_INTEGRATION") == "" {
		t.Skip("set ARDX_INTEGRATION=1 to run")
	}
	cli, err := FindArduinoCLI()
	if err != nil {
		t.Skip(err)
	}

	for _, standalone := range []bool{false, true} {
		name := "loose"
		if standalone {
			name = "standalone"
		}
		t.Run(name, func(t *testing.T) {
			project := t.TempDir()
			if standalone {
				project = filepath.Join(project, "blinker_demo")
				os.MkdirAll(project, 0o755)
			}
			writeZip(t, filepath.Join(project, "lib", "Blinker-0.1.0.zip"), map[string]string{
				"Blinker-0.1.0/library.properties": "name=Blinker\nversion=0.1.0\narchitectures=*\n",
				"Blinker-0.1.0/src/Blinker.h":      "#pragma once\n#include <Arduino.h>\ninline void blink(int pin) { digitalWrite(pin, !digitalRead(pin)); }\n",
			})
			os.WriteFile(filepath.Join(project, "blinker_demo.ino"),
				[]byte("#include <Blinker.h>\nvoid setup() { pinMode(13, OUTPUT); }\nvoid loop() { blink(13); delay(500); }\n"), 0o644)

			sketch, err := NewSketch(filepath.Join(project, "blinker_demo.ino"))
			if err != nil {
				t.Fatal(err)
			}
			job := NewJob(cli, sketch, "", "arduino:avr:uno", filepath.Join(project, "lib"), project)
			for _, s := range job.Stages()[:3] { // libraries, core, compile
				detail, err := s.Run(job)
				if err != nil {
					t.Fatalf("%s: %v\n%s", s.Name, err, strings.Join(job.LogTail(s.Name, 30), "\n"))
				}
				t.Logf("%-9s %s", s.Name, detail)
			}
		})
	}
}

func TestIntegration_CompileErrorPointsAtOriginalFile(t *testing.T) {
	if os.Getenv("ARDX_INTEGRATION") == "" {
		t.Skip("set ARDX_INTEGRATION=1 to run")
	}
	cli, err := FindArduinoCLI()
	if err != nil {
		t.Skip(err)
	}
	project := t.TempDir()
	path := filepath.Join(project, "broken.ino")
	os.WriteFile(path, []byte("void setup() {\n  undefinedThing();\n}\nvoid loop() {}\n"), 0o644)
	sketch, _ := NewSketch(path)
	job := NewJob(cli, sketch, "", "arduino:avr:uno", filepath.Join(project, "lib"), project)
	stages := job.Stages()
	for _, s := range stages[:2] {
		if _, err := s.Run(job); err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
	}
	if _, err := stages[2].Run(job); err == nil {
		t.Fatal("expected compile to fail")
	}
	log := strings.Join(job.LogTail("compile", 50), "\n")
	t.Log(log)
	if !strings.Contains(log, sketch.Path+":2:") {
		t.Errorf("expected error to reference %s:2", sketch.Path)
	}
	if strings.Contains(log, filepath.Join(WorkDirName, "sketches")) {
		t.Errorf("log still references the staged copy")
	}
}

// writeZip creates a zip file at path containing the given files.
func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}
