//go:build !windows

package cli

import "os"

func replaceOutputFile(source, destination string) error {
	return os.Rename(source, destination)
}
