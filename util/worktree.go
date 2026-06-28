package util

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
)

type WorktreeManager struct {
	Workspace    string
	worktreeDir  string
}

func NewWorktreeManager(workspace string) *WorktreeManager {
	worktreeDir := filepath.Join(filepath.Dir(workspace), ".quietforge_worktrees")
	return &WorktreeManager{Workspace: workspace, worktreeDir: worktreeDir}
}

func (m *WorktreeManager) runGit(args ...string) (string, string, int) {
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

func (m *WorktreeManager) Spawn(name string) *string {
	if _, _, code := m.runGit("rev-parse", "--is-inside-work-tree"); code != 0 {
		return nil
	}

	os.MkdirAll(m.worktreeDir, 0755)
	targetPath := filepath.Join(m.worktreeDir, name)

	if _, err := os.Stat(targetPath); err == nil {
		return &targetPath
	}

	if _, _, code := m.runGit("worktree", "add", "-b", name, targetPath); code == 0 {
		return &targetPath
	}

	if _, _, code := m.runGit("worktree", "add", targetPath, name); code == 0 {
		return &targetPath
	}

	return nil
}

func (m *WorktreeManager) Cleanup(name string) bool {
	targetPath := filepath.Join(m.worktreeDir, name)
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		return false
	}

	if _, _, code := m.runGit("worktree", "remove", "-f", targetPath); code == 0 {
		m.runGit("branch", "-D", name)
		return true
	}
	return false
}
