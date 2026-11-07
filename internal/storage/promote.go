package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// promoteStagedDirectory retains the old directory until promotion succeeds.
// On failure the backup is restored; if restoration fails it remains on disk.
func promoteStagedDirectory(staging, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	backup := ""
	if _, err := os.Lstat(dest); err == nil {
		backup, err = os.MkdirTemp(filepath.Dir(dest), ".tidy-backup-*")
		if err != nil {
			return err
		}
		// MkdirTemp creates an empty dir; Rename needs backup as a
		// non-existent path. Clean up the dir first, but on failure the
		// temp dir must not leak.
		if err := os.Remove(backup); err != nil {
			_ = os.RemoveAll(backup)
			return err
		}
		if err := os.Rename(dest, backup); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staging, dest); err != nil {
		if backup != "" {
			if restoreErr := os.Rename(backup, dest); restoreErr != nil {
				return fmt.Errorf("promotion failed: %w; restore failed: %v; original data retained at %s", err, restoreErr, backup)
			}
		}
		return err
	}
	if backup != "" {
		// Success: remove the old copy. On error, surface it instead of
		// leaking a stray backup dir in the destination's parent.
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("promotion succeeded but backup cleanup at %s failed: %w", backup, err)
		}
	}
	return nil
}
