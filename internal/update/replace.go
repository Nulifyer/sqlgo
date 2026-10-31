package update

import (
	"errors"
	"fmt"
	"os"
)

// replaceWindows is shared with host tests so rollback failures are exercised
// even when the test host cannot execute Windows binaries.
func replaceWindows(staged, target string, rename func(string, string) error, remove func(string) error) error {
	backup := target + ".old"
	if err := remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove previous backup (close older sqlgo instances): %w", err)
	}
	if err := rename(target, backup); err != nil {
		return fmt.Errorf("back up executable: %w", err)
	}
	if err := rename(staged, target); err != nil {
		if rollback := rename(backup, target); rollback != nil {
			return fmt.Errorf("install failed: %v; restore %s to %s manually: %w", err, backup, target, rollback)
		}
		return fmt.Errorf("install failed; previous binary restored: %w", err)
	}
	_ = remove(backup) // A running image stays locked until this process exits.
	return nil
}
