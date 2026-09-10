# Tidy

[![Go Report Card](https://goreportcard.com/badge/github.com/Kenzi-Siaufandi/tidy)](https://goreportcard.com/report/github.com/Kenzi-Siaufandi/tidy)

A tiny, lightweight, zero-dependency Go CLI designed to act as a **pre-flight container orchestrator** inside Pterodactyl Minecraft server containers (targeting Java 25 runtimes).

Tidy synchronizes declarative server configurations from Git, resolves and downloads server software via PaperMC Fill API, resolves and downloads plugins via Modrinth and direct URLs with cryptographic hash verification, and performs mustache-style `{{VAR_NAME}}` environment variable substitutions for database and plugin setups before the server launches.

---

## Features

- **Zero Runtime Dependencies**: Compiles to a single static binary (`CGO_ENABLED=0`, stripped, ~7MB) with no external system dependencies.
- **Git Config Synchronization**: Shallow-clones declarative configs on first install using system `git`.
- **PaperMC Fill API Resolution**: Resolves Paper builds (e.g. Paper `26.2`), downloads the upstream jar into the root directory, and strictly verifies SHA256 checksums. Direct `url` is disallowed under `[server]`.
- **Modrinth API Resolution**: Fetches plugins via Modrinth v2 API with a compliant `User-Agent: Kenzi-Siaufandi/tidy/0.1.0 (https://github.com/Kenzi-Siaufandi)` header, places jars into `./plugins/`, and verifies SHA512/SHA1 hashes.
- **Direct URL Downloads**: Downloads plugins with mandatory `sha256` checksum verification.
- **Atomic File Placement**: Streams downloads through hashers into `.tidy-tmp` files and only moves them into place upon hash verification.
- **Mustache Config Templating**: Scans `.yml`, `.yaml`, `.properties`, `.json`, `.conf`, `.toml` files and replaces `{{VAR_NAME}}` placeholders with container environment variables (e.g. `{{DB_HOST}}`, `{{DB_PASSWORD}}`).
- **State Tracking**: Writes `.tidy/state.json` recording installed artifacts and commit SHAs to prepare for subsequent container boots.

---

## Configuration (`tidy.toml`)

Place `tidy.toml` in your server root or Git configuration repository:

```toml
[server]
# Enforces fill API resolution; direct 'url' is not supported here
project = "paper"
version = "26.2"

[plugins.FastAsyncWorldEdit]
source = "modrinth"
project_id = "fastasyncworldedit"
version = "2.11.2"

[plugins.Vault]
source = "url"
url = "https://github.com/MilkBowl/Vault/releases/download/1.7.3/Vault.jar"
sha256 = "a6b5ed97f43a5cf5bbaf00a7c8cd23c5afc9bd003f849875af8b36e6cf77d01d"
```

---

## Usage in Pterodactyl Container

### Pterodactyl Startup Script / Entrypoint
```bash
#!/bin/ash
# Pre-flight container orchestration
./tidy

# Start Minecraft server with Java 25
exec java -Xms${INIT_MEMORY}M -Xmx${MAX_MEMORY}M -jar $(ls paper-*.jar 2>/dev/null || echo "server.jar")
```

### Environment Variables
| Variable | Description |
|---|---|
| `GIT_REPO` / `TIDY_GIT_REPO` | Git repository containing declarative server configs |
| `GIT_BRANCH` / `TIDY_GIT_BRANCH` | Git branch (default: `main`) |
| `GIT_TOKEN` / `GITHUB_TOKEN` | Authentication token for private Git repositories |
| `DB_HOST`, `DB_USER`, `DB_PASSWORD`, ... | Environment variables substituted into `{{VAR_NAME}}` |
| `MODRINTH_USER_AGENT` | Custom User-Agent override for Modrinth API |

---

## CLI Flags

```
  -config string
        Path to tidy.toml configuration file (default "tidy.toml")
  -workdir string
        Working directory for Minecraft server root (default ".")
  -git-repo string
        Git repository URL to synchronize declarative configs (or env GIT_REPO)
  -git-branch string
        Git branch to synchronize (default "main")
  -git-token string
        Git authentication token (or env GIT_TOKEN / GITHUB_TOKEN)
  -git-user string
        Git username (or env GIT_USER)
  -skip-git
        Skip Git synchronization
  -skip-templates
        Skip Mustache template variable replacement
  -version
        Print Tidy version and exit
```

---

## Building from Source

```bash
# Run all tests
make test

# Build static binary
make build
```
