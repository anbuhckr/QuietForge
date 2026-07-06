package implement

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"quietforge/storage"
	"quietforge/tool"
	"quietforge/util"
)

const maxWriteSize = 10 * 1024 * 1024 // 10MB cap per file

type WriteTool struct{}

func (t *WriteTool) ID() string {
	return "write"
}

func (t *WriteTool) Description() string {
	return "Write a new file or overwrite an existing file. Creates parent directories automatically."
}

func (t *WriteTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath": map[string]interface{}{"type": "string", "description": "Absolute path to the file to write"},
			"content":  map[string]interface{}{"type": "string", "description": "The content to write to the file"},
		},
		"required": []string{"filePath", "content"},
	}
}

func (t *WriteTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		FilePath string `json:"filePath"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	if len(params.Content) > maxWriteSize {
		return &tool.ToolResult{
			Error: "file_too_large",
			Output: fmt.Sprintf("Content is too large (%d bytes). Maximum: %d bytes. Consider writing the file in smaller sections.", len(params.Content), maxWriteSize),
		}, nil
	}

	pathStr, err := util.JailPath(ctx.Workspace, params.FilePath)
	if err != nil {
		return &tool.ToolResult{Error: "access_denied", Output: err.Error()}, nil
	}

	if err := os.MkdirAll(filepath.Dir(pathStr), 0755); err != nil {
		return &tool.ToolResult{Error: "write_error", Output: fmt.Sprintf("Failed to create directories: %v", err)}, nil
	}

	// Preserve original permissions on overwrite, default to 0644 for new files
	perm := os.FileMode(0644)
	if info, err := os.Stat(pathStr); err == nil {
		perm = info.Mode().Perm()
	}

	// Atomic write: temp file + rename (crash-safe)
	data := []byte(params.Content)
	if err := atomicWriteFile(pathStr, data, perm); err != nil {
		return &tool.ToolResult{Error: "write_error", Output: fmt.Sprintf("Failed to write file: %v", err)}, nil
	}

	if repo, ok := ctx.Extra["repo"].(*storage.Repository); ok && repo != nil {
		tool.GlobalLspManager.NotifyFileChanged(ctx.Workspace, pathStr, params.Content, repo)
	}

	relPath := pathStr
	if r, err := filepath.Rel(ctx.Workspace, pathStr); err == nil {
		relPath = r
	}

	return &tool.ToolResult{
		Title:  fmt.Sprintf("Written %d bytes to %s", len(params.Content), relPath),
		Output: fmt.Sprintf("File written: %s", relPath),
		Metadata: map[string]interface{}{
			"size": len(params.Content),
		},
	}, nil
}

// atomicWriteFile writes data to a temp file in the same directory, then
// atomically renames it to path. If the process crashes before rename,
// the original file is untouched.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".quietforge_tmp_*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	// Always clean up temp file on error
	cleanup := func() {
		_ = os.Remove(tmpPath)
	}
	success := false
	defer func() {
		if !success {
			cleanup()
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
    	return err
	}
	// Sync for durability before rename (power-failure safe)
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	// Set permissions before rename
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}

	// Atomic rename (same filesystem)
	if err := os.Rename(tmpPath, path); err != nil {
		if linkErr, ok := err.(*os.LinkError); ok && isCrossDevice(linkErr) {
			if copyErr := copyFile(tmpPath, path, perm); copyErr != nil {
				return fmt.Errorf("rename and cross-device copy both failed: %w", copyErr)
			}
			if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else {
			return err
		}
	}

	// Best-effort sync of the parent directory so the rename is durable.
	if dirFile, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}

	success = true
	return nil
}

// copyFile performs a non-atomic copy from src to dst as fallback for cross-device writes.
func copyFile(src, dst string, perm os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}
	return dstFile.Sync()
}

// isCrossDevice checks whether a LinkError is caused by a cross-device rename.
func isCrossDevice(err *os.LinkError) bool {
	if errno, ok := err.Err.(syscall.Errno); ok {
		if runtime.GOOS == "windows" {
			// ERROR_NOT_SAME_DEVICE = 17
			return errno == 17
		}
		// EXDEV on Unix (Linux, macOS, etc.)
		return errno == syscall.EXDEV
	}
	return false
}
