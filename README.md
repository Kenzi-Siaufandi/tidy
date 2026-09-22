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
