package resolver

import (
	"context"
	"strings"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
)

// ServerResolver abstracts server-jar resolution across upstream APIs
// (PaperMC Fill, PurpurMC). Both record SHA-256 in state for restart
// idempotency regardless of the upstream checksum algorithm.
type ServerResolver interface {
	Name() string
	FetchLatestBuildID(ctx context.Context, serverCfg config.ServerConfig) (int, error)
	ResolveAndDownload(ctx context.Context, serverCfg config.ServerConfig, workDir string) (*ServerDownloadResult, error)
}

// IsPurpurProject reports whether a project routes to the PurpurMC API.
func IsPurpurProject(project string) bool {
	return strings.EqualFold(strings.TrimSpace(project), "purpur")
}

// NewServerResolver returns the resolver for a configured server project.
func NewServerResolver(project string) ServerResolver {
	if IsPurpurProject(project) {
		return NewPurpurClient("", nil)
	}
	return NewPaperClient("", nil)
}

// Name identifies the upstream API for log lines.
func (c *PaperClient) Name() string { return "PaperMC Fill API" }

// FetchLatestBuildID resolves the newest Fill build ID for a project version.
func (c *PaperClient) FetchLatestBuildID(ctx context.Context, serverCfg config.ServerConfig) (int, error) {
	serverCfg.Build = "latest"
	resp, _, err := c.FetchBuild(ctx, serverCfg)
	if err != nil {
		return 0, err
	}
	return resp.ID, nil
}
