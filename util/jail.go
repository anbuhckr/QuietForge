package util

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var GlobalWorkspacesRoot string

func InitWorkspacesRoot() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	exeDir := filepath.Dir(exePath)
	GlobalWorkspacesRoot = filepath.Join(exeDir, ".quietforge", "workspaces")
	if err := os.MkdirAll(GlobalWorkspacesRoot, 0755); err != nil {
		return err
	}
	return nil
}

// JailPath cleans the requested path and ensures it does not escape the workspace.
// It returns the absolute jailed path if valid, or an error if it attempts to escape.
func JailPath(workspaceRoot string, requestedPath string) (string, error) {
	if workspaceRoot == "" {
		return "", fmt.Errorf("workspace root is empty")
	}

	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", err
	}
	absRoot = filepath.Clean(absRoot)

	absRequested := requestedPath
	if !filepath.IsAbs(absRequested) {
		absRequested = filepath.Join(absRoot, absRequested)
	}
	absRequested = filepath.Clean(absRequested)

	// Robust prefix check for Windows
	lowerRoot := strings.ToLower(filepath.ToSlash(absRoot))
	lowerReq := strings.ToLower(filepath.ToSlash(absRequested))

	// Ensure the boundary is strictly the directory, not a substring matching directory (e.g. /ws and /ws2)
	if !strings.HasSuffix(lowerRoot, "/") {
		lowerRoot += "/"
	}

	// If they are exactly the same directory, it's allowed
	if lowerReq == strings.TrimSuffix(lowerRoot, "/") {
		return absRequested, nil
	}

	if !strings.HasPrefix(lowerReq, lowerRoot) {
		return "", fmt.Errorf("access denied: path escapes workspace boundary")
	}

	return absRequested, nil
}
