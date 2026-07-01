package config

import (
	"os"
	"path/filepath"
	"runtime"
)

func GlobalConfigDir() string {
	var base string

	if runtime.GOOS == "windows" {
		base = os.Getenv("APPDATA")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, "AppData", "Roaming")
		}
	} else {
		base = os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".config")
		}
	}

	return filepath.Join(base, "quietforge")
}

func GlobalDataDir() string {
	var base string

	if runtime.GOOS == "windows" {
		base = os.Getenv("LOCALAPPDATA")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, "AppData", "Local")
		}
	} else {
		base = os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".local", "share")
		}
	}

	return filepath.Join(base, "quietforge")
}

func GlobalConfigFile() string {
	configDir := GlobalConfigDir()

	candidates := []string{
		"quietforge.jsonc",
		"quietforge.json",
		"config.json",
	}

	for _, c := range candidates {
		p := filepath.Join(configDir, c)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return filepath.Join(configDir, "quietforge.json")
}

func ProjectConfigFiles(startDir string) []string {
	var results []string

	current, err := filepath.Abs(startDir)
	if err != nil {
		current = startDir
	}

	for {
		for _, name := range []string{
			filepath.Join(".quietforge", "config.json"),
		} {
			p := filepath.Join(current, name)
			if _, err := os.Stat(p); err == nil {
				results = append(results, p)
			}
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return results
}

func ProjectDirectories(startDir string) []string {
	var dirs []string

	current, err := filepath.Abs(startDir)
	if err != nil {
		current = startDir
	}

	for {
		qfDir := filepath.Join(current, ".quietforge")

		if info, err := os.Stat(qfDir); err == nil && info.IsDir() {
			dirs = append(dirs, qfDir)
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return dirs
}

func PlansDir(worktree string, vcsType string) string {
	if vcsType == "" {
		vcsType = "git"
	}

	if vcsType == "git" {
		return filepath.Join(worktree, ".quietforge", "plans")
	}

	return filepath.Join(GlobalDataDir(), "plans")
}