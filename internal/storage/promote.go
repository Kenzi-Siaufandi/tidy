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
		if err := os.Remove(backup); err != nil {
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
		return os.RemoveAll(backup)
	}
	return nil
}
