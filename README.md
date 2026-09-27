# Tidy

> [!WARNING]
> **Notice**: This is a **vibe coded** project and is only meant to be used for convenience. Use at your own discretion.

A tiny, lightweight, zero-dependency Go CLI that acts as a **pre-flight container orchestrator** for Minecraft servers.

Tidy reads your `tidy.toml`, downloads the server jar, plugins, worlds, and other files you declared, fills in config values from environment variables, and gets everything ready before the server starts.

---

## Setup Helper

Prepare a server directory for Git-backed configs before first sync:

```bash
tidy setup init                # git init + write .gitignore/.gitattributes
tidy setup status              # list detectable configs and ignored files
tidy setup check               # audit: fail if worlds/JARs/DBs are tracked

tidy setup init --force --workdir /path/to/server
```

### Packing a world template

`pack-world` copies a world, strips player data (`playerdata/`, `stats/`,
`advancements/`, `session.lock`, `uid.dat`), and writes a tidy-compatible
archive. The live world is never modified. Output includes the SHA-256 and a
ready-to-paste `[worlds.*]` snippet. Stop the server first so region files are
consistent.

```bash
tidy setup pack-world --world world --output hub-world-v1.tar.gz
tidy setup pack-world --world world_nether --output nether.zip --format zip
```

### Creating tidy.toml

`new` is an interactive wizard: asks for the server (project/version/build),
then loops over plugins (modrinth, url, or local; Enter skips/finishes).
Output is validated before writing and never overwrites without `--force`.

```bash
tidy setup new --workdir /path/to/server
```

---

## Git Workflow (uploading configs to the remote)

Tidy treats git as one-way: the **remote is source of truth**. Every run
does `fetch` + `reset --hard`, so panel/local edits that aren't committed
and pushed are **discarded** (saved first to `.tidy/drift/drift-*.txt` for review).
The workflow is: edit locally → commit → push → restart container (or run `tidy`).

What belongs in git vs. what doesn't:

- Commit: `tidy.toml`, `server.properties`, `paper-global.yml`, `plugins/*/config.yml`, etc.
- Never commit: jars, `world/`, `world_nether/`, `logs/`, `cache/`, `.tidy/`, secrets. `tidy setup init` writes a `.gitignore` that already covers these. Verify with `tidy setup check`.

### One-time setup

```bash
cd /path/to/server

# 1. Init ignore rules + git repo (safe to re-run; --force overwrites ignores)
tidy setup init
tidy setup status   # what will be tracked
tidy setup check    # fails if a world/jar/db is tracked; fix with `git rm --cached <path>`

# 2. First commit
git add tidy.toml server.properties paper-global.yml plugins/
git commit -m "feat(server): initial tidy configs"
git branch -M main

# 3. Create an empty repo on GitHub/GitLab/Gitea, then push
git remote add origin https://github.com/YOU/mc-configs.git
git push -u origin main
```

For a private repo, use a fine-grained PAT with read-only `contents` access. Never put the token in the URL or `tidy.toml` — pass it at runtime (Tidy sends it via `GIT_ASKPASS`, the stored remote URL stays clean).

### Point Tidy at the remote

```bash
# Flags (override env):
tidy --git-repo https://github.com/YOU/mc-configs.git --git-branch main

# Or env (what Pterodactyl uses — set in the panel):
export GIT_REPO="https://github.com/YOU/mc-configs.git"
export GIT_BRANCH="main"                       # default is `main`
export GIT_TOKEN="ghp_..."                     # or GIT_AUTH_TOKEN / GITHUB_TOKEN
export GIT_USER="you"                          # optional, only for HTTP auth
tidy
```

First boot into a non-empty dir (e.g. Pterodactyl `/home/container`) does an in-place `init + fetch --depth 1 + checkout`, not a `git clone`. Later boots `reset --hard` then fast-forward. Use `--skip-git` to run fully local (keeps last commit in state).

### Daily edits

```bash
# Edit, review, push from your PC:
git status --short
git add <changed configs>
git commit -m "fix(shop): raise sell price"
git push

# Then restart the server / re-run tidy to pull.
# Local drift? Check what the last sync discarded:
cat .tidy/drift/drift-*.txt | tail -n 100
# Full console output of any run (plain text, one file per run, never pruned):
ls .tidy/log/
```

Notes:

- Templated `{{VAR}}` files and the `server.properties` date header (`#Sun Sep 20 ...`, rewritten every boot) are expected-dirty and never count as drift.
- Missing env var leaves `{{VAR}}` in place + warning, not fatal — set it in the panel before relying on the value.
- `.tidy/` state, drift reports, and run logs are local-only and always ignored.

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

# Or track the newest compatible release (errors if none supports the filter):
# [plugins.LuckPerms]
# source = "modrinth"
# project_id = "luckperms"
# version = "latest"      # default when omitted
# game_version = "1.21.1" # Minecraft version filter
# loader = "paper"        # defaults to "paper"
# channel = "release"     # "release" (default) | "beta" | "alpha"

[plugins.Vault]
source = "url"
url = "https://github.com/MilkBowl/Vault/releases/download/1.7.3/Vault.jar"
sha256 = "a6b5ed97f43a5cf5bbaf00a7c8cd23c5afc9bd003f849875af8b36e6cf77d01d"

# GitHub release asset, incl. private repos (auth via --git-token or env
# GIT_TOKEN / GIT_AUTH_TOKEN / GITHUB_TOKEN — never put the token in tidy.toml):
# [plugins.MyPrivatePlugin]
# source = "github"
# repo = "MyOrg/MyPrivatePlugin"
# tag = "latest"              # default when omitted; or pin an exact tag
# asset = "MyPrivatePlugin.jar" # exact release asset filename
# sha256 = "a6b5ed97f43a5cf5bbaf00a7c8cd23c5afc9bd003f849875af8b36e6cf77d01d" # mandatory

# Manually-uploaded jar (verified, never downloaded):
# [plugins.MyCustomPlugin]
# source = "local"
# path = "plugins/MyCustomPlugin.jar"
# sha256 = "a6b5ed97f43a5cf5bbaf00a7c8cd23c5afc9bd003f849875af8b36e6cf77d01d"

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

## Building from Source

```bash
# Run all tests
make test

# Build static binary
make build
```
