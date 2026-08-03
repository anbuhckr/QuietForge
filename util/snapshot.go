package util

import (
	"bytes"
	"fmt"
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

func (m *SnapshotManager) Create(message, refName string) *string {
	if _, _, code := m.runGit("rev-parse", "--is-inside-work-tree"); code != 0 {
		return nil
	}

	// Use a shadow index so we don't touch the user's staging area
	shadowIndex := ".git/qf_index"
	
	// Create a temporary index with all files (including untracked)
	cmdAdd := exec.Command("git", "add", "-A")
	cmdAdd.Dir = m.Workspace
	cmdAdd.Env = append(os.Environ(), "GIT_INDEX_FILE="+shadowIndex)
	// Ignore errors since some files might be locked or have permission issues
	cmdAdd.Run()

	// Write the shadow index to a tree
	cmdTree := exec.Command("git", "write-tree")
	cmdTree.Dir = m.Workspace
	cmdTree.Env = append(os.Environ(), "GIT_INDEX_FILE="+shadowIndex)
	treeBytes, err := cmdTree.Output()
	if err != nil {
		return nil
	}
	treeHash := strings.TrimSpace(string(treeBytes))

	// Create a commit from the tree
	cmdCommit := exec.Command("git", "commit-tree", treeHash, "-p", "HEAD", "-m", message)
	cmdCommit.Dir = m.Workspace
	cmdCommit.Env = append(os.Environ(), "GIT_INDEX_FILE="+shadowIndex)
	commitBytes, err := cmdCommit.Output()
	
	// Clean up the shadow index
	os.Remove(filepath.Join(m.Workspace, shadowIndex))

	if err == nil {
		hash := strings.TrimSpace(string(commitBytes))
		
		// Create the hidden ref
		refPath := "refs/agent/" + refName
		m.runGit("update-ref", refPath, hash)
		
		return &hash
	}

	stdout, _, code := m.runGit("rev-parse", "HEAD")
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
	_, _, code = m.runGit("restore", "--source="+commitHash, "--worktree", ".")
	return code == 0
}

func (m *SnapshotManager) RestoreFile(preRef, postRef, filePath string, force bool) (bool, error) {
	_, _, code := m.runGit("rev-parse", "--is-inside-work-tree")
	if code != 0 {
		return false, nil
	}

	// Check if user manually modified it after the postRef
	if postRef != "" && !force {
		_, _, diffCode := m.runGit("diff", "--quiet", postRef, "--", filePath)
		if diffCode != 0 { // 1 means changes exist
			return false, fmt.Errorf("USER_EDITED")
		}
	}

	stdout, stderr, code := m.runGit("restore", "--source="+preRef, "--worktree", filePath)
	if code == 0 {
		return true, nil
	}

	// If restore failed because pathspec did not match, it means the file was created recently.
	// Reverting it means we should delete it.
	lowerStderr := strings.ToLower(stderr)
	lowerStdout := strings.ToLower(stdout)
	if strings.Contains(lowerStderr, "pathspec") || strings.Contains(lowerStderr, "did not match") || strings.Contains(lowerStdout, "did not match") {
		fullPath := filepath.Join(m.Workspace, filePath)
		if _, err := os.Stat(fullPath); err == nil {
			os.Remove(fullPath)
			return true, nil
		}
	}

	return false, nil
}

func (m *SnapshotManager) CleanUntracked(commitHash string) {
	stdout, _, code := m.runGit("ls-tree", "-r", "--name-only", commitHash)
	if code != 0 {
		return
	}
	snapshotFiles := make(map[string]bool)
	for _, f := range strings.Split(stdout, "\n") {
		f = strings.TrimSpace(f)
		if f != "" {
			snapshotFiles[f] = true
		}
	}

	stdoutTracked, _, code := m.runGit("ls-files")
	if code != 0 {
		return
	}
	stdoutUntracked, _, code := m.runGit("ls-files", "--others", "--exclude-standard")
	if code != 0 {
		return
	}

	currentFiles := append(strings.Split(stdoutTracked, "\n"), strings.Split(stdoutUntracked, "\n")...)
	for _, f := range currentFiles {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !snapshotFiles[f] {
			os.Remove(filepath.Join(m.Workspace, f))
		}
	}
}
