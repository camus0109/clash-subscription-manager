//go:build windows

package handlers

import (
	"time"

	"golang.org/x/sys/windows"
)

const (
	// Windows readers opened without FILE_SHARE_DELETE temporarily prevent an
	// atomic replacement. Keep retrying the same MoveFileEx operation for a
	// bounded interval so normal short-lived readers do not turn a valid write
	// into an application error.
	maxReplaceAttempts = 24
	replaceRetryDelay  = 5 * time.Millisecond
)

func replaceFile(source, destination string) error {
	sourcePtr, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < maxReplaceAttempts; attempt++ {
		lastErr = windows.MoveFileEx(sourcePtr, destinationPtr, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if lastErr == nil {
			return nil
		}
		if lastErr != windows.ERROR_ACCESS_DENIED && lastErr != windows.ERROR_SHARING_VIOLATION {
			return lastErr
		}
		if attempt+1 < maxReplaceAttempts {
			time.Sleep(replaceRetryDelay)
		}
	}
	return lastErr
}
