#!/bin/bash
#
# Tidy pre-flight entrypoint for Pterodactyl.
#
# Runs `tidy` once, then execs $STARTUP.
# The Egg startup stays pure java (e.g. `java ... -jar {{SERVER_JAR}}`).
#
# Why not `tidy && exec java...` in STARTUP?
# Stock Yolks /entrypoint.sh ends with `exec env ${PARSED}` (unquoted expansion,
# no eval), so shell operators (`&&`, `;`, `$()`, quotes) from STARTUP are passed
# literally to `env` instead of being interpreted. Chained startups get mangled
# (PARSED truncates to just `tidy`). The final `eval` below preserves them.

# Default the TZ environment variable to UTC.
TZ=${TZ:-UTC}
export TZ

# Set environment variable that holds the Internal Docker IP
INTERNAL_IP=$(ip route get 1 | awk '{print $(NF-2);exit}')
export INTERNAL_IP

# Switch to the container's working directory
cd /home/container || exit 1

# 0. Honor TIDY_VERSION at runtime. The baked /usr/local/bin/tidy floats with
# the :java25 tag (latest main), and the Egg install script skips its own
# TIDY_VERSION download when tidy is pre-installed — so a pin would otherwise
# be silently ignored on every boot. A pinned version downloads that release
# binary (sha256-verified) and runs it instead of the baked one.
TIDY_BIN="$(command -v tidy 2>/dev/null || echo ./tidy)"
WANT="${TIDY_VERSION:-latest}"
if [ -n "${WANT}" ] && [ "${WANT}" != "latest" ]; then
  case "${WANT}" in
    v*) TAG="${WANT}" ;;
    *) TAG="v${WANT}" ;;
  esac
  HAVE="$("${TIDY_BIN}" --version 2>/dev/null | grep -oE 'v[0-9][^[:space:]]*' | head -n 1 || true)"
  if [ "${HAVE}" = "${TAG}" ]; then
    echo "tidy ${TAG} (baked binary matches TIDY_VERSION)"
  else
    ARCH="$(uname -m)"
    case "${ARCH}" in
      x86_64|amd64) GOARCH=amd64 ;;
      aarch64|arm64) GOARCH=arm64 ;;
      *) echo "[!] Unsupported architecture: ${ARCH}"; exit 1 ;;
    esac
    PIN_URL="https://github.com/Kenzi-Siaufandi/tidy/releases/download/${TAG}/tidy-${TAG}-linux-${GOARCH}"
    PIN_DEST="${TMPDIR:-/tmp}/tidy-${TAG}-linux-${GOARCH}"
    if [ -x "${PIN_DEST}" ] && "${PIN_DEST}" --version 2>/dev/null | grep -q "${TAG}"; then
      echo "tidy ${TAG} (reusing cached ${PIN_DEST})"
    else
      echo "TIDY_VERSION=${WANT}: downloading ${PIN_URL} ..."
      if curl -fsSL -o "${PIN_DEST}" "${PIN_URL}" && curl -fsSL -o "${PIN_DEST}.sha256" "${PIN_URL}.sha256"; then
        EXPECTED="$(cut -d ' ' -f 1 "${PIN_DEST}.sha256" | tr -d '\r\n')"
        ACTUAL="$(sha256sum "${PIN_DEST}" | cut -d ' ' -f 1)"
        rm -f "${PIN_DEST}.sha256"
        if [ "${EXPECTED}" != "${ACTUAL}" ]; then
          echo "[!] Checksum mismatch for tidy ${TAG}: expected ${EXPECTED}, got ${ACTUAL}"
          rm -f "${PIN_DEST}"
          exit 1
        fi
        chmod +x "${PIN_DEST}"
      else
        echo "[!] Failed to download tidy ${TAG}; refusing to fall back to latest"
        rm -f "${PIN_DEST}" "${PIN_DEST}.sha256"
        exit 1
      fi
    fi
    TIDY_BIN="${PIN_DEST}"
  fi
fi

# 1. Pre-flight via tidy (unless STARTUP already invokes it, for backwards compat
# with old `tidy && ...` eggs — running it twice is only wasted time otherwise).
MODIFIED_STARTUP=$(echo -e "${STARTUP}" | sed -e 's/{{/${/g' -e 's/}}/}/g')
TRIMMED="$(echo "${MODIFIED_STARTUP}" | sed -e 's/^[[:space:]]*//')"
case "${TRIMMED}" in
  tidy\ *|tidy\;*|tidy\&\&*|"$TIDY_BIN"*|TIDY_BIN*|\$TIDY_BIN*|\"\$TIDY_BIN\"*)
    printf "\033[1m\033[33mcontainer@pterodactyl~ \033[0m%s\n" "tidy (via Startup, skipping auto pre-flight)"
    ;;
  *)
    printf "\033[1m\033[33mcontainer@pterodactyl~ \033[0m%s\n" "tidy (auto pre-flight)"
    "${TIDY_BIN}"
    TIDY_RC=$?
    if [ ${TIDY_RC} -ne 0 ]; then
      echo "[!] tidy failed with exit code ${TIDY_RC}, not starting server"
      exit ${TIDY_RC}
    fi
    ;;
esac

# 2. Run the Egg startup. eval is required so `&&`, `;`, `$()`, `${VAR}` work.
# Note: no leading `exec` on the eval itself — `exec env a && exec b` would
# replace the shell at `a` and never reach `b`. Chained startups already carry
# their own inner `exec java`, which is what must become PID 1.
printf "\033[1m\033[33mcontainer@pterodactyl~ \033[0m%s\n" "${MODIFIED_STARTUP}"
case "${MODIFIED_STARTUP}" in
  *exec[[:space:]]*)
    # shellcheck disable=SC2086
    eval "${MODIFIED_STARTUP}"
    ;;
  *)
    # shellcheck disable=SC2086
    eval "exec env ${MODIFIED_STARTUP}"
    ;;
esac
