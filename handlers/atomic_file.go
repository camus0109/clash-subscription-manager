package handlers

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// atomicWriteFile writes data to a private temporary file in the destination
// directory, flushes it, and replaces the destination as one filesystem
// operation. Keeping the temporary file beside the destination is required for
// rename replacement to remain atomic.
func atomicWriteFile(filename string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(filename)
	tmp, err := os.CreateTemp(dir, ".clash-subscription-manager-*")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("no such file or directory: %w", err)
		}
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set temporary file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := replaceFile(tmpName, filename); err != nil {
		return fmt.Errorf("replace %q: %w", filename, err)
	}
	removeTemp = false
	return nil
}

// WritePrivateFile persists sensitive application data through the same
// atomic, private-permission path used for subscription state and caches.
func WritePrivateFile(filename string, data []byte) error {
	return atomicWriteFile(filename, data, 0600)
}
