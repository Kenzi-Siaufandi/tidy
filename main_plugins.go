package main

import (
	"strings"
	"time"

	"github.com/Kenzi-Siaufandi/tidy/internal/state"
)

// newPluginCheckpoint preserves ownership after a later reconciliation error.
// The copy deliberately retains unprocessed plugins, file state and template
// metadata; only a fully successful run replaces them with final state.
func newPluginCheckpoint(previous *state.State, server state.ServerState, commit string) func(string, string, state.PluginState) error {
	pending := state.State{InstalledAt: time.Now().UTC()}
	if previous != nil {
		pending = *previous
	}
	pending.Plugins = make(map[string]state.PluginState)
	if previous != nil {
		for name, plugin := range previous.Plugins {
			pending.Plugins[name] = plugin
		}
	}
	pending.Server = server
	if commit != "" {
		pending.GitCommit = commit
	}
	return func(workDir, name string, plugin state.PluginState) error {
		pending.Plugins[name] = plugin
		pending.LastSyncedAt = time.Now().UTC()
		return state.SaveState(workDir, &pending)
	}
}

// retirePluginArtifact cleans up an owned remote jar after its replacement
// succeeds, even when the replacement uses a different source.
func retirePluginArtifact(workDir string, old *state.PluginState, newPath string) {
	if old == nil || strings.EqualFold(strings.TrimSpace(old.Source), "local") {
		return
	}
	safeRemoveOldFile(workDir, old.Filename, newPath, true)
}
