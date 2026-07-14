package util

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func EnsureDir(path string) string {
	os.MkdirAll(path, 0755)
	return path
}

func GetTempDir() string {
	dir, err := os.MkdirTemp("", "quietforge_")
	if err != nil {
		return os.TempDir()
	}
	return dir
}

func ResolvePath(path string, baseDir ...string) string {
	if filepath.IsAbs(path) {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			resolved = filepath.Clean(path)
		}
		return resolved
	}
	base := "."
	if len(baseDir) > 0 && baseDir[0] != "" {
		base = baseDir[0]
	}
	abs, err := filepath.Abs(filepath.Join(base, path))
	if err != nil {
		return filepath.Clean(filepath.Join(base, path))
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return resolved
}

func SafePath(path string, allowedDirs ...[]string) bool {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	resolved = filepath.Clean(resolved)

	if len(allowedDirs) == 0 || len(allowedDirs[0]) == 0 {
		return true
	}
	for _, allowed := range allowedDirs[0] {
		allowedAbs, err := filepath.Abs(allowed)
		if err != nil {
			continue
		}
		allowedAbs = filepath.Clean(allowedAbs)
		rel, err := filepath.Rel(allowedAbs, resolved)
		if err == nil && !isDotDot(rel) {
			return true
		}
	}
	return false
}

func IsWithinProject(path string, projectDir ...string) bool {
	project := "."
	if len(projectDir) > 0 && projectDir[0] != "" {
		project = projectDir[0]
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absProject, err := filepath.Abs(project)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absProject, absPath)
	if err != nil {
		return false
	}
	return !isDotDot(rel)
}

func isDotDot(p string) bool {
	return p == ".." || (len(p) > 2 && p[:3] == ".."+string(filepath.Separator))
}

func TakeDirSnapshot(workspace string) map[string]int64 {
	snap := make(map[string]int64)
	
	// Get tracked files
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = workspace
	out, _ := cmd.Output()
	files := strings.Split(string(out), "\n")
	
	// Get untracked files
	cmdUntracked := exec.Command("git", "ls-files", "-o", "--exclude-standard")
	cmdUntracked.Dir = workspace
	outUntracked, _ := cmdUntracked.Output()
	files = append(files, strings.Split(string(outUntracked), "\n")...)

	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		fullPath := filepath.Join(workspace, f)
		info, err := os.Stat(fullPath)
		if err == nil {
			snap[f] = info.ModTime().UnixNano()
		}
	}
	return snap
}

func GetChangedFiles(before, after map[string]int64) []string {
	var changed []string
	for f, afterTime := range after {
		if beforeTime, ok := before[f]; !ok || afterTime != beforeTime {
			changed = append(changed, f)
		}
	}
	for f := range before {
		if _, ok := after[f]; !ok {
			changed = append(changed, f) // Deleted file
		}
	}
	return changed
}

func IsFileTracked(workspace, file string) bool {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", file)
	cmd.Dir = workspace
	err := cmd.Run()
	return err == nil
}
