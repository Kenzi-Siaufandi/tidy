package main

import (
	"strings"

	"github.com/Kenzi-Siaufandi/tidy/internal/state"
)

// retirePluginArtifact cleans up an owned remote jar after its replacement
// succeeds, even when the replacement uses a different source.
func retirePluginArtifact(workDir string, old *state.PluginState, newPath string) {
	if old == nil || strings.EqualFold(strings.TrimSpace(old.Source), "local") {
		return
	}
	safeRemoveOldFile(workDir, old.Filename, newPath, true)
}
