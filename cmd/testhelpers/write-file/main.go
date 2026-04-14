// Command write-file writes content to a file safely.
// It validates the target path is under the workspace root to prevent
// directory traversal attacks, creates parent directories as needed,
// and writes content atomically via a temporary file and rename.
//
// Usage: write-file <path> <content>
//
// The workspace root defaults to /workspace and can be overridden with
// the WORKSPACE_ROOT environment variable.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: write-file <path> <content>\n")
		os.Exit(1)
	}

	targetPath := os.Args[1]
	content := os.Args[2]

	workspaceRoot := os.Getenv("WORKSPACE_ROOT")
	if workspaceRoot == "" {
		workspaceRoot = "/workspace"
	}

	if err := writeFile(workspaceRoot, targetPath, content); err != nil {
		fmt.Fprintf(os.Stderr, "write-file: %v\n", err)
		os.Exit(1)
	}
}

// writeFile validates that targetPath is under workspaceRoot and writes content to it atomically.
func writeFile(workspaceRoot, targetPath, content string) error {
	// Resolve workspace root to an absolute, clean path.
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return fmt.Errorf("resolving workspace root %q: %w", workspaceRoot, err)
	}

	// Resolve the target path. If it is relative, treat it as relative to the workspace root.
	var absTarget string
	if filepath.IsAbs(targetPath) {
		absTarget = filepath.Clean(targetPath)
	} else {
		absTarget = filepath.Clean(filepath.Join(absRoot, targetPath))
	}

	// Ensure the target is under the workspace root.
	if !strings.HasPrefix(absTarget+string(filepath.Separator), absRoot+string(filepath.Separator)) {
		return fmt.Errorf("path %q is outside workspace root %q", targetPath, workspaceRoot)
	}

	// Create parent directories.
	if err := os.MkdirAll(filepath.Dir(absTarget), 0o755); err != nil {
		return fmt.Errorf("creating parent directories for %q: %w", absTarget, err)
	}

	// Write atomically: write to temp file in same directory, then rename.
	dir := filepath.Dir(absTarget)
	tmpFile, err := os.CreateTemp(dir, ".write-file-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %q: %w", dir, err)
	}
	tmpName := tmpFile.Name()

	// Clean up temp file on failure.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.WriteString(content); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing content to temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpName, absTarget); err != nil {
		return fmt.Errorf("renaming temp file to %q: %w", absTarget, err)
	}

	success = true
	return nil
}
