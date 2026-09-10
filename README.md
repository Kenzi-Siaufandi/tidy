# Tidy

A tiny, lightweight, zero-dependency Go CLI designed to act as a **pre-flight container orchestrator** inside Pterodactyl Minecraft server containers (targeting Java 25 runtimes).

Tidy synchronizes declarative server configurations from Git, reconciles changes across container restarts, resolves and downloads server software via PaperMC Fill API, resolves and downloads plugins via Modrinth and direct URLs with universal SHA-256 cryptographic verification, acquires large files and worlds over direct HTTPS with stream archive extraction, and performs mustache-style `{{VAR_NAME}}` environment variable substitutions for database and plugin setups before the server launches.

---

## Features

- **Zero Runtime Dependencies**: Compiles to a single static binary (`CGO_ENABLED=0`, stripped, ~7MB) with no external system dependencies.
- **Git Config Synchronization & Incremental Pull**:
  - Shallow-clones declarative configs on first install.
  - Automatically resets working tree credential changes before pulling on restart, guaranteeing clean, conflict-free `git pull`.
  - Reports incoming commits (`git log`) and modified files (`[M]`, `[A]`, `[D]`).
- **Universal SHA-256 Verification**:
  - Strict SHA-256 checksum enforcement across all server builds, plugin downloads, worlds, and large files.
  - On restart, verifies existing local files against expected SHA-256 hashes to skip redundant downloads.
- **PaperMC Fill API Resolution**: Resolves Paper builds (e.g. Paper `26.2`), downloads the upstream jar into the root directory, and strictly verifies SHA-256 checksums. Direct `url` is disallowed under `[server]`.
- **Modrinth API Resolution**: Fetches plugins via Modrinth v2 API with a compliant `User-Agent: Kenzi-Siaufandi/tidy/0.1.0 (https://github.com/Kenzi-Siaufandi)` header, places jars into `./plugins/`, and verifies hashes.
- **Large Files & World Management (`[files.<name>]` / `[worlds.<name>]`)**:
  - Direct HTTPS streaming downloads with mandatory SHA-256 checksums.
  - Destination `path` directory parameter (e.g. `path = "world"`, `path = "world_nether"`).
  - Stream archive extraction (`extract = true`) for `.zip`, `.tar.gz`, `.tgz`, and `.tar` using Go standard library.
  - **Player data protection (`once = true` by default)**: Only downloads if the target directory does not exist, protecting player survival progress from being wiped on container restart.
  - Automatically detects and removes stale `session.lock` files left over from unclean container terminations.
- **Mustache Config Templating**: Scans `.yml`, `.yaml`, `.properties`, `.json`, `.conf`, `.toml` files and replaces `{{VAR_NAME}}` placeholders with container environment variables (e.g. `{{DB_HOST}}`, `{{DB_PASSWORD}}`).
- **State Tracking**: Writes `.tidy/state.json` recording installed artifacts, commit SHAs, and SHA-256 hashes across container boots.

---

## Configuration (`tidy.toml`)

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

# --- Phase 2: Large Files & Worlds (Direct HTTPS with Mandatory SHA-256) ---

[files.overworld]
source = "http"
path = "world"                                                # Destination directory
url = "https://downloads.example.com/worlds/hub-world-v1.tar.gz"
sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" # Mandatory SHA-256
extract = true                                                # Automatically unpacks into path
once = true                                                   # Protect player data: only download if path missing

[worlds.nether]
source = "http"
path = "world_nether"
url = "https://downloads.example.com/worlds/nether-template.zip"
sha256 = "b8a8b167520e5c9a0bc43fdfb0d4c1b48b6f3c1b1842e47856d117a56114a1e9"
extract = true
once = true
```

---

---

## Pterodactyl Deployment & Docker Image

### Official Pre-Baked Docker Image
A lightweight, production-ready container image targeting Java 25 is published directly to GitHub Container Registry (GHCR):

```text
ghcr.io/kenzi-siaufandi/tidy:java25
```

- **Pre-installed**: Java 25, Git, CA Certificates, Curl, and Tidy (`/usr/local/bin/tidy`).
- **Instant Boot**: Zero install delays; no network calls to fetch binaries during container boot.
- **CI/CD**: Automatically built and published on every commit via GitHub Actions.

### Pterodactyl Custom Egg (`egg-tidy-paper.json`)
Import [egg-tidy-paper.json](file:///home/exig/Projects/GoProjects/Tidy/egg-tidy-paper.json) into your Pterodactyl panel (**Admin -> Nests -> Minecraft -> Import Egg**).

#### Startup Command:
```bash
tidy && exec java -Xms128M -XX:MaxRAMPercentage=95.0 -Dterminal.jline=false -Dterminal.ansi=true -jar $(ls paper-*.jar 2>/dev/null || echo "{{SERVER_JARFILE}}")
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

## Building from Source

```bash
# Run all tests
make test

# Build static binary
make build
```

---

> [!WARNING]
> **Notice**: This is a **vibe coded** project and is only meant to be used for convenience. Use at your own discretion.

