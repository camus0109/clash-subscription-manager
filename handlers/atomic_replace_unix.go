//go:build !windows

package handlers

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
