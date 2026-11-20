package lib

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// writeFile writes content to root/rel, creating parent folders.
func writeFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ──────────────────────────────────────────────────────────────────────────────
// FindSketches
// ──────────────────────────────────────────────────────────────────────────────

func TestFindSketches(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "zeta.ino", "")
	writeFile(t, root, "alpha.ino", "")
	writeFile(t, root, "notes.txt", "")
	writeFile(t, root, "blink/blink.ino", "")
	writeFile(t, root, "misc/other.ino", "")         // folder name doesn't match: skipped
	writeFile(t, root, "lib/lib.ino", "")            // lib dir: skipped
	writeFile(t, root, ".ardx/sketches/x/x.ino", "") // hidden: skipped

	sketches, err := FindSketches(root, "lib")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range sketches {
		got = append(got, s.Rel(root))
	}
	want := []string{"alpha.ino", "zeta.ino", filepath.Join("blink", "blink.ino")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// NewSketch
// ──────────────────────────────────────────────────────────────────────────────

func TestNewSketch_FromFolder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "blink/blink.ino", "")
	s, err := NewSketch(filepath.Join(root, "blink"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "blink" || !s.IsStandalone() {
		t.Errorf("unexpected sketch %+v", s)
	}
}

func TestNewSketch_Errors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "readme.txt", "")
	os.MkdirAll(filepath.Join(root, "empty"), 0o755)
	for _, p := range []string{"missing.ino", "readme.txt", "empty"} {
		if _, err := NewSketch(filepath.Join(root, p)); err == nil {
			t.Errorf("expected error for %s", p)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Stage
// ──────────────────────────────────────────────────────────────────────────────

func TestStage_StandaloneUsesFolderAsIs(t *testing.T) {
	root := t.TempDir()
	s, _ := NewSketch(writeFile(t, root, "blink/blink.ino", ""))
	dir, err := s.Stage(filepath.Join(root, "stage"))
	if err != nil {
		t.Fatal(err)
	}
	if dir != s.Dir {
		t.Errorf("expected %s, got %s", s.Dir, dir)
	}
}

func TestStage_LooseSketchCopiedWithSiblings(t *testing.T) {
	root := t.TempDir()
	s, _ := NewSketch(writeFile(t, root, "Blink.INO", "void setup(){}"))
	writeFile(t, root, "pins.h", "")
	writeFile(t, root, "helper.cpp", "")
	writeFile(t, root, "other.ino", "") // other sketches must not be merged in

	dir, err := s.Stage(filepath.Join(root, "stage"))
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(root, "stage", "Blink") {
		t.Errorf("unexpected stage dir %s", dir)
	}
	for _, f := range []string{"Blink.ino", "pins.h", "helper.cpp"} {
		if !fileExists(filepath.Join(dir, f)) {
			t.Errorf("expected %s in staged sketch", f)
		}
	}
	if fileExists(filepath.Join(dir, "other.ino")) {
		t.Error("other.ino should not be staged")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Includes
// ──────────────────────────────────────────────────────────────────────────────

func TestIncludes_UniqueAcrossFiles(t *testing.T) {
	root := t.TempDir()
	s, _ := NewSketch(writeFile(t, root, "blink.ino", `#include <Arduino.h>
  #  include "pins.h"
#include <TM1637Display.h> // display
// #include <NotReally.h> commented out, ignored
#include <Arduino.h>
`))
	writeFile(t, root, "pins.h", "#include <Wire.h>\n")

	got, err := s.Includes()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Arduino.h", "pins.h", "TM1637Display.h", "Wire.h"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
