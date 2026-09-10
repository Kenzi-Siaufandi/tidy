package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const StateFilename = ".tidy/state.json"

// State stores persistent tracking metadata about installed software and plugins.
type State struct {
	InstalledAt  time.Time              `json:"installed_at"`
	LastSyncedAt time.Time              `json:"last_synced_at"`
	GitCommit    string                 `json:"git_commit,omitempty"`
	Server       ServerState            `json:"server"`
	Plugins      map[string]PluginState `json:"plugins"`
}

// ServerState records the active server software metadata.
type ServerState struct {
	Project  string `json:"project"`
	Version  string `json:"version"`
	BuildID  int    `json:"build_id"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
}

// PluginState records installed plugin metadata and verification hashes.
type PluginState struct {
	Source   string `json:"source"`
	Filename string `json:"filename"`
	Version  string `json:"version,omitempty"`
	HashAlgo string `json:"hash_algo"`
	Hash     string `json:"hash"`
}

// IsFirstInstall checks whether Tidy has run and completed an install previously.
func IsFirstInstall(workDir string) bool {
	statePath := filepath.Join(workDir, StateFilename)
	_, err := os.Stat(statePath)
	return errors.Is(err, os.ErrNotExist)
}

// LoadState reads the state file from the given work directory.
func LoadState(workDir string) (*State, error) {
	statePath := filepath.Join(workDir, StateFilename)
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	if s.Plugins == nil {
		s.Plugins = make(map[string]PluginState)
	}

	return &s, nil
}

// SaveState commits the state metadata to .tidy/state.json.
func SaveState(workDir string, s *State) error {
	dir := filepath.Join(workDir, ".tidy")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	statePath := filepath.Join(workDir, StateFilename)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize state: %w", err)
	}

	if err := os.WriteFile(statePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}
