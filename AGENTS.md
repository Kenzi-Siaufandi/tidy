# AGENTS.md — Tidy

Single static Go CLI (`main.go` only entrypoint). Flow: git sync → load `tidy.toml` → server → plugins → files/worlds → templates → save `.tidy/state.json`.

## Commands

- `make test` (`go test -v ./...`) — all unit tests, mocked HTTP, no services needed.
- Focused: `go test ./internal/<pkg> -run TestName -v` (e.g. `internal/config`, `internal/resolver`).
- `make build` → `CGO_ENABLED=0 go build -ldflags="-s -w" -o tidy main.go` (builds `main.go`, not `./...`).
- No lint / typecheck / codegen config.

## Layout

- `internal/config/` — `tidy.toml` parse + `Validate()`. `tidy.toml.example` is the reference.
- `internal/git/` — requires system `git` binary; shallow `--depth 1` only.
- `internal/resolver/` — PaperMC Fill, Modrinth v2, generic `DownloadAndVerify`, `SafeFilename`/`SafeArchiveName` traversal guards.
- `internal/storage/` — `[files.*]`/`[worlds.*]` sync + stdlib-only extract (`.zip`, `.tar.gz`, `.tgz`, `.tar`).
- `internal/templating/` — `{{VAR}}` replacement.
- `internal/state/` — `.tidy/state.json` tracking; `IsFirstInstall` = file missing.
- `internal/version/version.go` — single version source (`const Version`); also builds Modrinth User-Agent. Don't hardcode version elsewhere.

## Config gotchas (`internal/config/config.go:Validate`)

- `[server]`: `url` is rejected; must use `project` + `version`. `build` defaults to `"latest"`.
- `plugins.<name>`: only `modrinth` (`project_id` + `version` required, `sha256` optional pin) or `url` (`url` + mandatory 64-char hex `sha256`).
- `files.<name>` / `worlds.<name>`: `path` + `url` + mandatory 64-char hex `sha256`. `source` accepts `http`/`https`/`url`/empty. Same name in both sections is an error.
- `FileConfig.Once` defaults to `true` when unset — `once=true` skips download if dest exists **without verifying hash** and records empty SHA in state.
- `extract=true` requires directory `path`; `extract=false` with dir `path` + `once=false` errors.
- `[templates]`: `disabled` skips; empty `paths` falls back to built-in glob list in `config.go`. Only `.yml/.yaml/.properties/.json/.conf/.toml/.txt/.cfg/.env` are processed; `.git`/`.tidy`/`cache` always skipped even if a custom glob matches.

## Runtime behavior agents miss

- Git: non-empty workdir (e.g. Pterodactyl `/home/container`) uses `cloneInPlace` (init + fetch + checkout), not `git clone`. `Pull` runs `reset --hard HEAD` first to discard templated changes. Auth via `GIT_ASKPASS` temp script — never embed tokens in URL/config. Env aliases: `GIT_REPO|GIT_URL|TIDY_GIT_REPO`, `GIT_BRANCH|TIDY_GIT_BRANCH` (default `main`), `GIT_TOKEN|GIT_AUTH_TOKEN|GITHUB_TOKEN`, `GIT_USER`. Flags (`--git-repo`, `--skip-git`, `--skip-templates`, `--config`, `--workdir`) override env.
- Idempotency: server/plugins skip download only if local file exists **and** SHA-256 verifies against state; stale server jar deleted only after new download succeeds. Removed plugins (in state, absent from config) are deleted from `plugins/`.
- Templates: missing env var leaves `{{VAR}}` in place + prints warning, not fatal. Writes are atomic (temp file + rename, preserves mode).
- Storage: `extract` downloads to `.tidy/<name>.archive.tmp`, extracts, deletes tmp, then removes stale `session.lock` under dest.
- Modrinth: requires `User-Agent` (default from `version.Version`, override via `MODRINTH_USER_AGENT`); picks `primary` file, prefers `sha512` > `sha256` > `sha1`, then always computes local SHA-256 and enforces `sha256` pin if set.

## Artifacts / CI

- `.gitignore`: `tidy` binary, `*.jar`, `*.tidy-tmp`, `plugins/`, `logs/`, `world/`, `cache/`, `.tidy/`, `.env*`, `test-workspace/`. Don't commit server artifacts or state.
- Docker: multi-stage `golang:1.27.1-alpine` → `ghcr.io/pterodactyl/yolks:java_25`. CI (`.github/workflows/docker.yml`) pushes `ghcr.io/<repo>:java25` on `main` push / `v*` tags. Startup: `tidy && exec java ...` (see `egg-tidy-paper.json`, panel-managed — don't hand-edit).
