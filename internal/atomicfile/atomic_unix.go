//go:build !windows

package atomicfile

import "os"

func replaceOutputFile(source, destination string) error {
	return os.Rename(source, destination)
}
