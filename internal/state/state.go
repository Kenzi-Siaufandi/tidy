package state

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const StateFilename = ".tidy/state.json"

// State stores persistent tracking metadata about installed software, plugins, and assets.
type State struct {
	InstalledAt  time.Time              `json:"installed_at"`
	LastSyncedAt time.Time              `json:"last_synced_at"`
	GitCommit    string                 `json:"git_commit,omitempty"`
	Server       ServerState            `json:"server"`
	Plugins      map[string]PluginState `json:"plugins"`
	Files        map[string]FileState   `json:"files,omitempty"`
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
	SHA256   string `json:"sha256,omitempty"` // always computed for local cache validation
}

// FileState records synchronized large files or world assets.
type FileState struct {
	Path      string    `json:"path"`
	URL       string    `json:"url"`
	SHA256    string    `json:"sha256"`
	SyncedAt  time.Time `json:"synced_at"`
	Extracted bool      `json:"extracted"`
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
	if s.Files == nil {
		s.Files = make(map[string]FileState)
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

// ComputeFileSHA256 streams the file and computes its lowercase hex SHA-256 hash.
func ComputeFileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyLocalSHA256 checks whether a local file exists and matches the expected SHA-256 hash.
func VerifyLocalSHA256(filePath, expectedSHA256 string) bool {
	if strings.TrimSpace(expectedSHA256) == "" {
		return false
	}

	computed, err := ComputeFileSHA256(filePath)
	if err != nil {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(computed), []byte(strings.ToLower(strings.TrimSpace(expectedSHA256)))) == 1
}
