//go:build windows

package handlers

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// readFile opens files with delete sharing so an atomic replacement can occur
// while a client is reading the previous complete version.
func readFile(filename string) ([]byte, error) {
	filenamePtr, err := windows.UTF16PtrFromString(filename)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		filenamePtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), filename)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("open %q: create file handle", filename)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	return data, closeErr
}
