//go:build !windows

package handlers

import "os"

func readFile(filename string) ([]byte, error) {
	return os.ReadFile(filename)
}
