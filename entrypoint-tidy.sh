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

# Print Java version (parity with stock Yolks entrypoint)
printf "\033[1m\033[33mcontainer@pterodactyl~ \033[0mjava -version\n"
java -version

# 1. Pre-flight via tidy (unless STARTUP already invokes it, for backwards compat
# with old `tidy && ...` eggs — running it twice is only wasted time otherwise).
TIDY_BIN="$(command -v tidy 2>/dev/null || echo ./tidy)"

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
