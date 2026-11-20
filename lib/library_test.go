package lib

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// ──────────────────────────────────────────────────────────────────────────────
// PrepareLibraries
// ──────────────────────────────────────────────────────────────────────────────

func TestPrepareLibraries_StripsWrapperAndUsesPropertiesName(t *testing.T) {
	root := t.TempDir()
	libDir := filepath.Join(root, "lib")
	writeZip(t, filepath.Join(libDir, "TM1637-1.2.0.zip"), map[string]string{
		"TM1637-1.2.0/library.properties":   "name=TM1637\nversion=1.2.0\n",
		"TM1637-1.2.0/TM1637Display.h":      "",
		"TM1637-1.2.0/TM1637Display.cpp":    "",
		"TM1637-1.2.0/examples/Demo/Demo.h": "",
		"__MACOSX/TM1637-1.2.0/._TM1637.h":  "",
	})

	libs, err := PrepareLibraries(libDir, filepath.Join(root, "out"))
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) != 1 {
		t.Fatalf("expected 1 library, got %d", len(libs))
	}
	l := libs[0]
	if l.Name != "TM1637" {
		t.Errorf("expected name TM1637, got %q", l.Name)
	}
	if want := filepath.Join(root, "out", "TM1637"); l.Dir != want {
		t.Errorf("expected dir %s, got %s", want, l.Dir)
	}
	if !fileExists(filepath.Join(l.Dir, "TM1637Display.cpp")) {
		t.Error("expected TM1637Display.cpp at library root")
	}
	if !reflect.DeepEqual(l.Headers, []string{"TM1637Display.h"}) {
		t.Errorf("expected headers to exclude examples, got %v", l.Headers)
	}
}

func TestPrepareLibraries_NoWrapperUsesZipName(t *testing.T) {
	root := t.TempDir()
	libDir := filepath.Join(root, "lib")
	writeZip(t, filepath.Join(libDir, "My Lib.zip"), map[string]string{
		"src/MyLib.h":         "",
		"src/utility/Extra.h": "",
		"README.md":           "",
	})
	libs, err := PrepareLibraries(libDir, filepath.Join(root, "out"))
	if err != nil {
		t.Fatal(err)
	}
	l := libs[0]
	if filepath.Base(l.Dir) != "My_Lib" {
		t.Errorf("expected sanitised folder My_Lib, got %s", filepath.Base(l.Dir))
	}
	if !reflect.DeepEqual(l.Headers, []string{"MyLib.h", "utility/Extra.h"}) {
		t.Errorf("unexpected headers %v", l.Headers)
	}
}

func TestPrepareLibraries_ReplacesPreviousExtraction(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	os.MkdirAll(filepath.Join(out, "Stale"), 0o755)
	if _, err := PrepareLibraries(filepath.Join(root, "lib"), out); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(out, "Stale")) {
		t.Error("expected stale library folder to be removed")
	}
}

func TestPrepareLibraries_MissingDirIsEmpty(t *testing.T) {
	root := t.TempDir()
	libs, err := PrepareLibraries(filepath.Join(root, "nope"), filepath.Join(root, "out"))
	if err != nil || len(libs) != 0 {
		t.Errorf("expected no libraries and no error, got %v, %v", libs, err)
	}
}

func TestPrepareLibraries_RejectsZipSlip(t *testing.T) {
	root := t.TempDir()
	libDir := filepath.Join(root, "lib")
	writeZip(t, filepath.Join(libDir, "evil.zip"), map[string]string{"../../escape.h": "x"})
	if _, err := PrepareLibraries(libDir, filepath.Join(root, "out")); err == nil {
		t.Error("expected an error for a path escaping the destination")
	}
	if fileExists(filepath.Join(root, "escape.h")) {
		t.Error("file escaped the destination folder")
	}
}

func TestPrepareLibraries_DuplicateLibraryErrors(t *testing.T) {
	root := t.TempDir()
	libDir := filepath.Join(root, "lib")
	for _, z := range []string{"a.zip", "b.zip"} {
		writeZip(t, filepath.Join(libDir, z), map[string]string{
			"Dup/library.properties": "name=Dup\n",
			"Dup/Dup.h":              "",
		})
	}
	if _, err := PrepareLibraries(libDir, filepath.Join(root, "out")); err == nil {
		t.Error("expected an error when two zips provide the same library")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// UsedLibraries
// ──────────────────────────────────────────────────────────────────────────────

func TestUsedLibraries(t *testing.T) {
	libs := []Library{
		{Name: "A", Headers: []string{"A.h"}},
		{Name: "B", Headers: []string{"B.h", "utility/B2.h"}},
		{Name: "C", Headers: []string{"C.h"}},
	}
	used := UsedLibraries(libs, []string{"Arduino.h", "utility/B2.h", "A.h"})
	if len(used) != 2 || used[0].Name != "A" || used[1].Name != "B" {
		t.Errorf("unexpected used libraries %+v", used)
	}
}
