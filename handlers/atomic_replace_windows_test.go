//go:build windows

package handlers

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestReplaceFileReturnsBoundedErrorForLockedDestination(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "destination")
	if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}

	destinationPtr, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(
		destinationPtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)

	started := time.Now()
	err = replaceFile(source, destination)
	if err == nil {
		t.Fatal("replaceFile() unexpectedly succeeded while destination was locked")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("replaceFile() exceeded bounded retry interval: %v", elapsed)
	}

	if content, readErr := os.ReadFile(source); readErr != nil || string(content) != "new" {
		t.Fatalf("source changed after failed replacement: content=%q err=%v", content, readErr)
	}
	if content, readErr := os.ReadFile(destination); readErr != nil || string(content) != "old" {
		t.Fatalf("destination changed after failed replacement: content=%q err=%v", content, readErr)
	}
}
