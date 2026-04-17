package widget

import "os"

// OverwriteGuard implements the two-Enter confirm pattern for
// overwriting an existing file. Callers gate their write on
// Check(path); the first call with an existing target arms the
// guard and returns false, signaling that the caller should
// render a "press Enter again" prompt. A second Check with the
// same path returns true and the write proceeds. Any different
// path (user edits between presses, caller retargets) re-arms.
//
// Files that don't exist pass through immediately -- there's
// nothing to overwrite, so no confirmation is needed.
type OverwriteGuard struct {
	armedPath string
}

// Check reports whether a write to path should proceed.
func (g *OverwriteGuard) Check(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return true
	}
	if g.armedPath == path {
		return true
	}
	g.armedPath = path
	return false
}

// Armed reports whether the guard is currently waiting for a
// confirming Enter. Used by callers to tailor hints / status.
func (g *OverwriteGuard) Armed() bool {
	return g.armedPath != ""
}

// ArmedPath returns the path the guard is armed against. Empty
// string when not armed.
func (g *OverwriteGuard) ArmedPath() string {
	return g.armedPath
}

// Reset clears the armed state. Call on any edit that could
// change the target so the user has to reconfirm.
func (g *OverwriteGuard) Reset() {
	g.armedPath = ""
}
