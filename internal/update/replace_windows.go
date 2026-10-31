package update

import "os"

// Windows keeps an in-use backup until a later update can remove it.
func replaceExecutable(staged, target string) error {
	return replaceWindows(staged, target, os.Rename, os.Remove)
}
