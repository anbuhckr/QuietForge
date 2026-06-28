package util

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type SnapshotManager struct {
	Workspace string
}

func NewSnapshotManager(workspace string) *SnapshotManager {
	return &SnapshotManager{Workspace: workspace}
}

func (m *SnapshotManager) runGit(args ...string) (string, string, int) {
	cmd := exec.Command("git", args...)
	cmd.Dir = m.Workspace
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			code = exitError.ExitCode()
		} else {
			code = 1
		}
	}
	return stdout.String(), stderr.String(), code
}

func (m *SnapshotManager) Create(message string) *string {
	if _, _, code := m.runGit("rev-parse", "--is-inside-work-tree"); code != 0 {
		return nil
	}

	stdout, _, code := m.runGit("stash", "create", "-m", message)
	if code == 0 && strings.TrimSpace(stdout) != "" {
		hash := strings.TrimSpace(stdout)
		return &hash
	}

	stdout, _, code = m.runGit("rev-parse", "HEAD")
	if code == 0 {
		hash := strings.TrimSpace(stdout)
		return &hash
	}
	return nil
}

func (m *SnapshotManager) Diff(commitHash string) *string {
	if _, _, code := m.runGit("rev-parse", "--is-inside-work-tree"); code != 0 {
		return nil
	}

	stdout, _, code := m.runGit("diff", commitHash)
	if code == 0 {
		s := stdout
		return &s
	}
	return nil
}

func (m *SnapshotManager) Restore(commitHash string) bool {
	_, _, code := m.runGit("rev-parse", "--is-inside-work-tree")
	if code != 0 {
		return false
	}
	_, _, code = m.runGit("checkout", commitHash, "--", ".")
	return code == 0
}

func (m *SnapshotManager) RestoreFile(commitHash string, filePath string) bool {
	_, _, code := m.runGit("rev-parse", "--is-inside-work-tree")
	if code != 0 {
		return false
	}

	stdout, stderr, code := m.runGit("checkout", commitHash, "--", filePath)
	if code == 0 {
		return true
	}

	// If checkout failed because pathspec did not match, it means the file was created recently.
	// Reverting it means we should delete it.
	lowerStderr := strings.ToLower(stderr)
	lowerStdout := strings.ToLower(stdout)
	if strings.Contains(lowerStderr, "pathspec") || strings.Contains(lowerStderr, "did not match") || strings.Contains(lowerStdout, "did not match") {
		fullPath := filepath.Join(m.Workspace, filePath)
		if _, err := os.Stat(fullPath); err == nil {
			os.Remove(fullPath)
			return true
		}
	}

	return false
}
