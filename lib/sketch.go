package lib

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// sketchSiblingExts are the extra source files arduino-cli compiles alongside
// a sketch's .ino file when they sit in the sketch folder.
var sketchSiblingExts = map[string]bool{".h": true, ".hpp": true, ".c": true, ".cpp": true, ".S": true}

// includeRe matches `#include <foo.h>` and `#include "foo.h"`.
var includeRe = regexp.MustCompile(`^\s*#\s*include\s*[<"]([^>"]+)[>"]`)

// Sketch is an .ino file that can be compiled and uploaded.
type Sketch struct {
	Name string // file name without extension, e.g. "blink"
	Path string // path to the .ino file
	Dir  string // folder containing the .ino file
}

// Rel returns the sketch path relative to root, for display.
func (s Sketch) Rel(root string) string {
	if rel, err := filepath.Rel(root, s.Path); err == nil {
		return rel
	}
	return s.Path
}

// FindSketches lists the .ino files in root plus those in immediate
// subfolders that follow the Arduino layout (<name>/<name>.ino). Hidden
// folders and libDir are skipped.
func FindSketches(root, libDir string) ([]Sketch, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var sketches []Sketch
	var subdirs []Sketch
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(root, name)
		if !e.IsDir() {
			if strings.EqualFold(filepath.Ext(name), ".ino") {
				sketches = append(sketches, newSketch(path))
			}
			continue
		}
		if sameDir(path, filepath.Join(root, libDir)) {
			continue
		}
		candidate := filepath.Join(path, name+".ino")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			subdirs = append(subdirs, newSketch(candidate))
		}
	}
	sort.Slice(sketches, func(i, j int) bool { return sketches[i].Path < sketches[j].Path })
	sort.Slice(subdirs, func(i, j int) bool { return subdirs[i].Path < subdirs[j].Path })
	return append(sketches, subdirs...), nil
}

// NewSketch builds a Sketch from a path to an .ino file or a sketch folder.
func NewSketch(path string) (Sketch, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Sketch{}, err
	}
	if info.IsDir() {
		path = filepath.Join(path, filepath.Base(filepath.Clean(path))+".ino")
		if _, err := os.Stat(path); err != nil {
			return Sketch{}, fmt.Errorf("no %s in sketch folder", filepath.Base(path))
		}
	} else if !strings.EqualFold(filepath.Ext(path), ".ino") {
		return Sketch{}, fmt.Errorf("%s is not an .ino file", path)
	}
	return newSketch(path), nil
}

func newSketch(path string) Sketch {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	base := filepath.Base(abs)
	return Sketch{
		Name: strings.TrimSuffix(base, filepath.Ext(base)),
		Path: abs,
		Dir:  filepath.Dir(abs),
	}
}

// IsStandalone reports whether the sketch already lives in a folder of the
// same name, which is what arduino-cli requires.
func (s Sketch) IsStandalone() bool {
	return filepath.Base(s.Dir) == s.Name
}

// SourceFiles returns the files that make up the sketch: the .ino itself
// plus any .h/.hpp/.c/.cpp/.S files beside it.
func (s Sketch) SourceFiles() ([]string, error) {
	files := []string{s.Path}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() && sketchSiblingExts[filepath.Ext(e.Name())] {
			files = append(files, filepath.Join(s.Dir, e.Name()))
		}
	}
	return files, nil
}

// Stage returns a folder arduino-cli can compile. If the sketch already sits
// in a folder of the same name that folder is used as-is; otherwise (e.g.
// blink.ino loose in the working directory) the sketch and its sibling
// source files are copied into stageRoot/<name>/<name>.ino.
func (s Sketch) Stage(stageRoot string) (string, error) {
	if s.IsStandalone() {
		return s.Dir, nil
	}
	dest := filepath.Join(stageRoot, s.Name)
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	files, err := s.SourceFiles()
	if err != nil {
		return "", err
	}
	for _, src := range files {
		name := filepath.Base(src)
		if src == s.Path {
			name = s.Name + ".ino"
		}
		if err := copyFile(src, filepath.Join(dest, name)); err != nil {
			return "", err
		}
	}
	return dest, nil
}

// Includes returns the unique headers #included by the sketch's source
// files, in the order they first appear.
func (s Sketch) Includes() ([]string, error) {
	files, err := s.SourceFiles()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var includes []string
	for _, f := range files {
		found, err := parseIncludes(f)
		if err != nil {
			return nil, err
		}
		for _, inc := range found {
			if !seen[inc] {
				seen[inc] = true
				includes = append(includes, inc)
			}
		}
	}
	return includes, nil
}

// parseIncludes returns the headers #included by a single file.
func parseIncludes(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var includes []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if m := includeRe.FindStringSubmatch(scanner.Text()); m != nil {
			includes = append(includes, strings.TrimSpace(m[1]))
		}
	}
	return includes, scanner.Err()
}

// copyFile copies src to dst, creating or truncating dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// sameDir reports whether a and b refer to the same directory path.
func sameDir(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}
