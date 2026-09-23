package setup

// DefaultGitignore is the recommended ignore set for tidy-managed servers.
const DefaultGitignore = `# =============================================================================
# Minecraft Server Configuration .gitignore (Tailored for tidy)
# =============================================================================

# --- Server Executables, Bundlers & Libraries ---
*.jar
libraries/
bundler/
versions/
cache/
.paper/
.purpur/
.fabric/
.quilt/

# --- World Directories & Data ---
/world/
/world_nether/
/world_the_end/
/world_the_end_nether/
**/region/
**/entities/
**/poi/
**/playerdata/
**/stats/
**/advancements/
**/level.dat*
**/session.lock
**/uid.dat
*.mca

# --- Runtime Databases & Data Storage ---
*.db
*.sqlite*
*.h2.db
*.mv.db
*.dat
!server-icon.png

# --- Runtime Caches, Sessions & Ephemeral Files ---
usercache.json
**/sessions/
**/session/
**/tmp/
**/temp/
**/updater/
**/cache/
**/backups/
**/backup/

# --- Logs & Crash Reports ---
logs/
crash-reports/
*.log
*.log.gz

# --- Binary Assets, Archives & World Exports ---
*.zip
*.tar
*.tar.gz
*.schem
*.schematic
*.litematic
*.dump
*.bin
*.class
*.exe

# --- OS & Editor Files ---
.DS_Store
Thumbs.db
.idea/
.vscode/

# --- Secrets & Private Keys (never commit) ---
*.key
*.pem

# --- Tidy Runtime State (never commit local sync state) ---
.tidy/
`

// DefaultGitattributes normalizes config line endings to LF.
const DefaultGitattributes = `# Normalize line endings to LF for configs
* text=auto eol=lf
*.yml text eol=lf
*.yaml text eol=lf
*.json text eol=lf
*.properties text eol=lf
*.toml text eol=lf
*.conf text eol=lf
*.txt text eol=lf
*.png binary
*.jar binary
*.db binary
*.sqlite binary
*.zip binary
`
