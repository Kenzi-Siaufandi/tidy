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
# (PARSED truncates to just `tidy`). `run_startup_safely` below preserves `&&`,
# `||`, `;` chaining plus quotes WITHOUT eval: only simple `${NAME}`/`$NAME`
# references expand to literal values; `$()`, backticks, `|`, `>`, `&`, globs
# stay literal arguments, so FLAG/SERVER_JAR values cannot inject commands.

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
MODIFIED_STARTUP=$(printf '%s' "${STARTUP}" | sed -e 's/{{/${/g' -e 's/}}/}/g')
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

# 2. Run the Egg startup WITHOUT eval (P0: FLAG/SERVER_JAR are untrusted).
# Only `&&`, `||`, `;` outside quotes chain segments; everything else (`$()`,
# backticks, `|`, `>`, `<`, `&`, `*?[]`, `~`, `$(( ))`) stays a literal argument.
# Note: no leading `exec` on a wrapper — `exec env a && exec b` would replace
# the shell at `a` and never reach `b`. Chained startups already carry their
# own inner `exec java`, which is what must become PID 1.
printf "\033[1m\033[33mcontainer@pterodactyl~ \033[0m%s\n" "${MODIFIED_STARTUP}"
set -f # disable pathname expansion: *?[] in values stay literal.

__tidy_argv=()
__tidy_last_rc=0
__tidy_pending_op=";"

__tidy_should_run_next() {
  case "${__tidy_pending_op}" in
    '&&') [ "${__tidy_last_rc}" -eq 0 ] ;;
    '||') [ "${__tidy_last_rc}" -ne 0 ] ;;
    *) return 0 ;;
  esac
}

__tidy_run_segment_now() {
  # Execute the current __tidy_argv as a child process (non-final segment).
  local -a cmd=()
  if [ "${#__tidy_argv[@]}" -eq 0 ]; then
    return 0
  fi
  if [ "${__tidy_argv[0]}" = "exec" ]; then
    if [ "${#__tidy_argv[@]}" -eq 1 ]; then
      __tidy_argv=()
      return 0
    fi
    cmd=("${__tidy_argv[@]:1}")
  else
    cmd=("${__tidy_argv[@]}")
  fi
  __tidy_argv=()
  if __tidy_should_run_next; then
    env "${cmd[@]}"
    __tidy_last_rc=$?
  fi
}

__tidy_exec_final_segment() {
  # Exec the final segment (replaces PID 1). Never returns on success.
  local -a cmd=()
  if [ "${#__tidy_argv[@]}" -eq 0 ]; then
    exit "${__tidy_last_rc}"
  fi
  if [ "${__tidy_argv[0]}" = "exec" ]; then
    if [ "${#__tidy_argv[@]}" -eq 1 ]; then
      exit "${__tidy_last_rc}"
    fi
    cmd=("${__tidy_argv[@]:1}")
  else
    cmd=("${__tidy_argv[@]}")
  fi
  if ! __tidy_should_run_next; then
    exit "${__tidy_last_rc}"
  fi
  exec env "${cmd[@]}"
  echo "[!] failed to exec startup command" >&2
  exit 127
}

# Tokenize $1 into argv segments, expanding ONLY simple variable references
# (`{{NAME}}`, `${NAME}`, `$NAME`) to literal env values. Values are inserted
# literally during tokenization: only whitespace splits unquoted values (so
# `FLAG="-Xmx2G -Dfoo=bar"` still works and empty FLAG vanishes); `;`, `&`,
# `|`, quotes, `$`, backslashes inside values never gain syntactic meaning.
run_startup_safely() {
  local raw="${1:-}"
  local token=""
  local token_active=0
  local in_single=0
  local in_double=0
  local i=0
  local n=${#raw}
  local ch nxt
  local j k name val m mlen d c

  __tidy_argv=()
  __tidy_last_rc=0
  __tidy_pending_op=";"

  while [ "${i}" -lt "${n}" ]; do
    ch="${raw:${i}:1}"

    # Inside single quotes: everything literal until closing '.
    if [ "${in_single}" -eq 1 ]; then
      if [ "${ch}" = "'" ]; then
        in_single=0
      else
        token="${token}${ch}"
      fi
      i=$((i + 1))
      continue
    fi

    # Inside double quotes: expand vars (no splitting), backslash escapes next.
    if [ "${in_double}" -eq 1 ]; then
      if [ "${ch}" = '"' ]; then
        in_double=0
        i=$((i + 1))
        continue
      fi
      if [ "${ch}" = "\\" ]; then
        if [ $((i + 1)) -lt "${n}" ]; then
          nxt="${raw:$((i + 1)):1}"
          token="${token}${nxt}"
          token_active=1
          i=$((i + 2))
        else
          token="${token}${ch}"
          token_active=1
          i=$((i + 1))
        fi
        continue
      fi
      if [ "${ch}" = "{" ]; then
        nxt="${raw:$((i + 1)):1}"
        if [ "${nxt}" = "{" ]; then
          j=$((i + 2))
          k="${j}"
          name=""
          while [ "${k}" -lt "${n}" ]; do
            c="${raw:${k}:1}"
            case "${c}" in
              [A-Za-z0-9_]) name="${name}${c}"; k=$((k + 1)) ;;
              *) break ;;
            esac
          done
          if [ -n "${name}" ]; then
            case "${name}" in
              [A-Za-z_]*)
                if [ "${raw:${k}:2}" = "}}" ]; then
                  val="${!name:-}"
                  token="${token}${val}"
                  token_active=1
                  i=$((k + 2))
                  continue
                fi
                ;;
            esac
          fi
        fi
        token="${token}${ch}"
        token_active=1
        i=$((i + 1))
        continue
      fi
      if [ "${ch}" = "$" ]; then
        nxt="${raw:$((i + 1)):1}"
        if [ "${nxt}" = "{" ]; then
          j=$((i + 2))
          k="${j}"
          name=""
          while [ "${k}" -lt "${n}" ]; do
            c="${raw:${k}:1}"
            case "${c}" in
              [A-Za-z0-9_]) name="${name}${c}"; k=$((k + 1)) ;;
              *) break ;;
            esac
          done
          if [ -n "${name}" ]; then
            case "${name}" in
              [A-Za-z_]*)
                if [ "${raw:${k}:1}" = "}" ]; then
                  val="${!name:-}"
                  token="${token}${val}"
                  token_active=1
                  i=$((k + 1))
                  continue
                fi
                ;;
            esac
          fi
        else
          case "${nxt}" in
            [A-Za-z_])
              j=$((i + 1))
              k="${j}"
              name=""
              while [ "${k}" -lt "${n}" ]; do
                c="${raw:${k}:1}"
                case "${c}" in
                  [A-Za-z0-9_]) name="${name}${c}"; k=$((k + 1)) ;;
                  *) break ;;
                esac
              done
              val="${!name:-}"
              token="${token}${val}"
              token_active=1
              i="${k}"
              continue
              ;;
          esac
        fi
        token="${token}\$"
        token_active=1
        i=$((i + 1))
        continue
      fi
      token="${token}${ch}"
      token_active=1
      i=$((i + 1))
      continue
    fi

    # Outside quotes.
    if [ "${ch}" = "'" ]; then
      in_single=1
      token_active=1
      i=$((i + 1))
      continue
    fi
    if [ "${ch}" = '"' ]; then
      in_double=1
      token_active=1
      i=$((i + 1))
      continue
    fi
    if [ "${ch}" = "\\" ]; then
      if [ $((i + 1)) -lt "${n}" ]; then
        nxt="${raw:$((i + 1)):1}"
        token="${token}${nxt}"
        token_active=1
        i=$((i + 2))
      else
        token="${token}${ch}"
        token_active=1
        i=$((i + 1))
      fi
      continue
    fi
    case "${ch}" in
      ' '|$'\t'|$'\n'|$'\r')
        if [ "${token_active}" -ne 0 ]; then
          __tidy_argv+=("${token}")
          token=""
          token_active=0
        fi
        i=$((i + 1))
        continue
        ;;
    esac
    if [ "${ch}" = ";" ]; then
      if [ "${token_active}" -ne 0 ]; then
        __tidy_argv+=("${token}")
        token=""
        token_active=0
      fi
      __tidy_run_segment_now
      __tidy_pending_op=";"
      i=$((i + 1))
      continue
    fi
    if [ "${ch}" = "&" ]; then
      nxt="${raw:$((i + 1)):1}"
      if [ "${nxt}" = "&" ]; then
        if [ "${token_active}" -ne 0 ]; then
          __tidy_argv+=("${token}")
          token=""
          token_active=0
        fi
        __tidy_run_segment_now
        __tidy_pending_op="&&"
        i=$((i + 2))
        continue
      fi
      token="${token}${ch}"
      token_active=1
      i=$((i + 1))
      continue
    fi
    if [ "${ch}" = "|" ]; then
      nxt="${raw:$((i + 1)):1}"
      if [ "${nxt}" = "|" ]; then
        if [ "${token_active}" -ne 0 ]; then
          __tidy_argv+=("${token}")
          token=""
          token_active=0
        fi
        __tidy_run_segment_now
        __tidy_pending_op="||"
        i=$((i + 2))
        continue
      fi
      token="${token}${ch}"
      token_active=1
      i=$((i + 1))
      continue
    fi
    if [ "${ch}" = "{" ]; then
      nxt="${raw:$((i + 1)):1}"
      if [ "${nxt}" = "{" ]; then
        j=$((i + 2))
        k="${j}"
        name=""
        while [ "${k}" -lt "${n}" ]; do
          c="${raw:${k}:1}"
          case "${c}" in
            [A-Za-z0-9_]) name="${name}${c}"; k=$((k + 1)) ;;
            *) break ;;
          esac
        done
        if [ -n "${name}" ]; then
          case "${name}" in
            [A-Za-z_]*)
              if [ "${raw:${k}:2}" = "}}" ]; then
                val="${!name:-}"
                m=0
                mlen=${#val}
                while [ "${m}" -lt "${mlen}" ]; do
                  d="${val:${m}:1}"
                  case "${d}" in
                    ' '|$'\t'|$'\n'|$'\r')
                      if [ "${token_active}" -ne 0 ]; then
                        __tidy_argv+=("${token}")
                        token=""
                        token_active=0
                      fi
                      ;;
                    *)
                      token="${token}${d}"
                      token_active=1
                      ;;
                  esac
                  m=$((m + 1))
                done
                i=$((k + 2))
                continue
              fi
              ;;
          esac
        fi
      fi
      token="${token}${ch}"
      token_active=1
      i=$((i + 1))
      continue
    fi
    if [ "${ch}" = "$" ]; then
      nxt="${raw:$((i + 1)):1}"
      if [ "${nxt}" = "{" ]; then
        j=$((i + 2))
        k="${j}"
        name=""
        while [ "${k}" -lt "${n}" ]; do
          c="${raw:${k}:1}"
          case "${c}" in
            [A-Za-z0-9_]) name="${name}${c}"; k=$((k + 1)) ;;
            *) break ;;
          esac
        done
        if [ -n "${name}" ]; then
          case "${name}" in
            [A-Za-z_]*)
              if [ "${raw:${k}:1}" = "}" ]; then
                val="${!name:-}"
                m=0
                mlen=${#val}
                while [ "${m}" -lt "${mlen}" ]; do
                  d="${val:${m}:1}"
                  case "${d}" in
                    ' '|$'\t'|$'\n'|$'\r')
                      if [ "${token_active}" -ne 0 ]; then
                        __tidy_argv+=("${token}")
                        token=""
                        token_active=0
                      fi
                      ;;
                    *)
                      token="${token}${d}"
                      token_active=1
                      ;;
                  esac
                  m=$((m + 1))
                done
                i=$((k + 1))
                continue
              fi
              ;;
          esac
        fi
      else
        case "${nxt}" in
          [A-Za-z_])
            j=$((i + 1))
            k="${j}"
            name=""
            while [ "${k}" -lt "${n}" ]; do
              c="${raw:${k}:1}"
              case "${c}" in
                [A-Za-z0-9_]) name="${name}${c}"; k=$((k + 1)) ;;
                *) break ;;
              esac
            done
            val="${!name:-}"
            m=0
            mlen=${#val}
            while [ "${m}" -lt "${mlen}" ]; do
              d="${val:${m}:1}"
              case "${d}" in
                ' '|$'\t'|$'\n'|$'\r')
                  if [ "${token_active}" -ne 0 ]; then
                    __tidy_argv+=("${token}")
                    token=""
                    token_active=0
                  fi
                  ;;
                *)
                  token="${token}${d}"
                  token_active=1
                  ;;
              esac
              m=$((m + 1))
            done
            i="${k}"
            continue
            ;;
        esac
      fi
      token="${token}\$"
      token_active=1
      i=$((i + 1))
      continue
    fi
    token="${token}${ch}"
    token_active=1
    i=$((i + 1))
  done

  if [ "${token_active}" -ne 0 ]; then
    __tidy_argv+=("${token}")
  fi
  __tidy_exec_final_segment
}

run_startup_safely "${MODIFIED_STARTUP}"
