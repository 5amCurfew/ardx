package lib

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Library is an Arduino library unpacked from a .zip in the lib directory,
// the same way the Arduino IDE's "Add .ZIP Library..." installs one.
type Library struct {
	Name    string   // display name, from library.properties when present
	Zip     string   // path to the source .zip
	Dir     string   // folder the library was extracted to
	Headers []string // headers it exposes, relative to its root or src/
}

// Provides reports whether the library exposes the given #include path.
func (l Library) Provides(include string) bool {
	for _, h := range l.Headers {
		if h == include {
			return true
		}
	}
	return false
}

// PrepareLibraries extracts every .zip in libDir into destDir/<library>/,
// replacing anything previously extracted there, so destDir can be handed to
// arduino-cli as a libraries collection. A missing libDir is not an error.
func PrepareLibraries(libDir, destDir string) ([]Library, error) {
	zips, err := filepath.Glob(filepath.Join(libDir, "*.zip"))
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(destDir); err != nil {
		return nil, err
	}
	if len(zips) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}
	sort.Strings(zips)
	libs := make([]Library, 0, len(zips))
	for _, z := range zips {
		lib, err := extractLibrary(z, destDir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(z), err)
		}
		libs = append(libs, lib)
	}
	return libs, nil
}

// UsedLibraries returns the libraries that provide at least one of includes.
func UsedLibraries(libs []Library, includes []string) []Library {
	var used []Library
	for _, l := range libs {
		for _, inc := range includes {
			if l.Provides(inc) {
				used = append(used, l)
				break
			}
		}
	}
	return used
}

// extractLibrary unpacks a library zip into destRoot. Most library zips
// (e.g. GitHub downloads) wrap everything in a single top-level folder such
// as "TM1637-1.2.0/"; that folder is stripped and its name used for the
// library. Zips without a wrapper folder are named after the zip file.
func extractLibrary(zipPath, destRoot string) (Library, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return Library{}, err
	}
	defer r.Close()

	var files []*zip.File
	for _, f := range r.File {
		if f.Name == "" || strings.HasPrefix(f.Name, "__MACOSX/") || filepath.Base(f.Name) == ".DS_Store" {
			continue
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return Library{}, fmt.Errorf("zip is empty")
	}

	prefix := commonRootFolder(files)
	dirName := strings.TrimSuffix(prefix, "/")
	if dirName == "" {
		dirName = strings.TrimSuffix(filepath.Base(zipPath), filepath.Ext(zipPath))
	}
	dirName = sanitizeLibraryName(dirName)
	dest := filepath.Join(destRoot, dirName)
	if _, err := os.Stat(dest); err == nil {
		return Library{}, fmt.Errorf("another zip in the lib directory already provides %s", dirName)
	}

	for _, f := range files {
		rel := strings.TrimPrefix(f.Name, prefix)
		if rel == "" {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		// Guard against "zip slip" entries escaping the destination.
		if target != dest && !strings.HasPrefix(target, dest+string(os.PathSeparator)) {
			return Library{}, fmt.Errorf("illegal path in zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return Library{}, err
			}
			continue
		}
		if err := extractZipFile(f, target); err != nil {
			return Library{}, err
		}
	}

	lib := Library{Name: dirName, Zip: zipPath, Dir: dest}
	if name := readLibraryName(filepath.Join(dest, "library.properties")); name != "" {
		lib.Name = name
		// Name the folder after the library rather than the zip's wrapper
		// folder (often "<repo>-<version>"), as the Library Manager does.
		renamed := filepath.Join(destRoot, sanitizeLibraryName(name))
		if renamed != dest {
			if _, err := os.Stat(renamed); err == nil {
				return Library{}, fmt.Errorf("another zip in the lib directory already provides %s", name)
			}
			if err := os.Rename(dest, renamed); err != nil {
				return Library{}, err
			}
			lib.Dir = renamed
		}
	}
	lib.Headers = listHeaders(lib.Dir)
	return lib, nil
}

// commonRootFolder returns "name/" if every entry sits under a single
// top-level folder, or "" otherwise.
func commonRootFolder(files []*zip.File) string {
	var root string
	for _, f := range files {
		name := f.Name
		i := strings.Index(name, "/")
		if i < 0 {
			return "" // a file at the zip root
		}
		top := name[:i+1]
		if root == "" {
			root = top
		} else if top != root {
			return ""
		}
	}
	return root
}

// extractZipFile writes a single zip entry to target.
func extractZipFile(f *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// readLibraryName returns the name= value from a library.properties file.
func readLibraryName(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if v, ok := strings.CutPrefix(line, "name="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// listHeaders lists the headers a library exposes: every .h/.hpp under src/
// (1.5 layout) or the library root (legacy layout, excluding examples/ and
// extras/), as include paths relative to that folder.
func listHeaders(dir string) []string {
	base := dir
	if info, err := os.Stat(filepath.Join(dir, "src")); err == nil && info.IsDir() {
		base = filepath.Join(dir, "src")
	}
	var headers []string
	filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != base && (strings.HasPrefix(name, ".") || name == "examples" || name == "extras" || name == "test" || name == "tests") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext == ".h" || ext == ".hpp" {
			if rel, err := filepath.Rel(base, path); err == nil {
				headers = append(headers, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(headers)
	return headers
}

// sanitizeLibraryName mirrors the Arduino IDE, which replaces characters
// that aren't valid in library folder names with underscores.
func sanitizeLibraryName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
