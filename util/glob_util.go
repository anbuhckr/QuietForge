package util

import (
	"os"
	"path/filepath"
	"strings"
)

func GlobMatch(pattern, filepathStr string) bool {
	parts := strings.Split(pattern, "/")
	target := strings.Split(strings.ReplaceAll(filepathStr, "\\", "/"), "/")
	if len(parts) == 0 || len(target) == 0 {
		return false
	}
	return matchParts(parts, target)
}

func matchParts(parts, target []string) bool {
	if len(parts) == 0 && len(target) == 0 {
		return true
	}
	if len(parts) == 0 {
		return false
	}
	if len(target) == 0 {
		for _, p := range parts {
			if p != "**" {
				return false
			}
		}
		return true
	}
	part := parts[0]
	if part == "**" {
		return matchParts(parts[1:], target) || matchParts(parts, target[1:])
	} else if matchSingle(part, target[0]) {
		return matchParts(parts[1:], target[1:])
	}
	return false
}

func matchSingle(pattern, text string) bool {
	if pattern == text {
		return true
	}
	if pattern == "*" {
		return true
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return false
	}
	if !strings.HasPrefix(text, parts[0]) {
		return false
	}
	text = text[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(text, parts[i])
		if idx < 0 {
			return false
		}
		text = text[idx+len(parts[i]):]
	}
	return strings.HasSuffix(text, parts[len(parts)-1])
}

func FindFiles(baseDir, pattern string, maxResults ...int) []string {
	max := 200
	if len(maxResults) > 0 && maxResults[0] > 0 {
		max = maxResults[0]
	}

	base, err := filepath.Abs(baseDir)
	if err != nil {
		return nil
	}
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return nil
	}

	var results []string

	if strings.Contains(pattern, "**") {
		parts := strings.SplitN(pattern, "**", 2)
		prefix := strings.TrimRight(parts[0], "/")
		suffix := strings.TrimLeft(parts[1], "/") + "/"

		searchDir := base
		if prefix != "" {
			searchDir = filepath.Join(base, prefix)
		}

		filepath.WalkDir(searchDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if len(results) >= max {
				return filepath.SkipAll
			}
			rel, _ := filepath.Rel(base, path)
			rel = filepath.ToSlash(rel)
			if suffix != "/" && !strings.HasSuffix(rel, strings.TrimRight(suffix, "/")) {
				return nil
			}
			results = append(results, rel)
			return nil
		})
	} else {
		matches, err := filepath.Glob(filepath.Join(base, pattern))
		if err != nil {
			return nil
		}
		for _, m := range matches {
			if len(results) >= max {
				break
			}
			rel, _ := filepath.Rel(base, m)
			results = append(results, filepath.ToSlash(rel))
		}
	}

	return results
}
