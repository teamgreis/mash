#!/usr/bin/env bash
# Phase 22 conformance gate runner.
#
# Spawns mash-py-device in two-port mode and drives mash-go's mash-test
# against it with --srv-resolve enabled (DEC-082).
#
# Usage:
#   tools/run_conformance.sh [--filter '<glob>'] [--files '<csv>'] [--exclude-tags '<csv>']
#
# Defaults match tests/conformance/test_against_go_runner.py.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null && pwd)"
MASH_PY_ROOT="$(cd -- "$SCRIPT_DIR/.." >/dev/null && pwd)"
REPO_ROOT="$(cd -- "$MASH_PY_ROOT/.." >/dev/null && pwd)"
MASH_GO_DIR="$REPO_ROOT/mash-go"

FILES_DEFAULT="commissioning-*,protocol-*,connection-*,read-*,subscription-*,bidirectional-read,bidirectional-subscribe,feature-interaction-*,message-*"
EXCLUDE_TAGS_DEFAULT="env:multi-device,slow"

FILTER=""
FILES="$FILES_DEFAULT"
EXCLUDE_TAGS="$EXCLUDE_TAGS_DEFAULT"
DISCRIMINATOR="${MASH_PHASE22_DISCRIMINATOR:-}"
SETUP_CODE="20202021"
PYTHON="${PYTHON:-}"
if [[ -z "$PYTHON" ]]; then
    if [[ -x "$MASH_PY_ROOT/.venv/bin/python" ]]; then
        PYTHON="$MASH_PY_ROOT/.venv/bin/python"
    else
        PYTHON="python3"
    fi
fi
LOG_LEVEL="info"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --filter) FILTER="$2"; shift 2 ;;
        --files) FILES="$2"; shift 2 ;;
        --exclude-tags) EXCLUDE_TAGS="$2"; shift 2 ;;
        --discriminator) DISCRIMINATOR="$2"; shift 2 ;;
        --setup-code) SETUP_CODE="$2"; shift 2 ;;
        --log-level) LOG_LEVEL="$2"; shift 2 ;;
        -h|--help)
            sed -n '2,12p' "$0"
            exit 0
            ;;
        *)
            echo "unknown flag: $1" >&2
            exit 64
            ;;
    esac
done

if ! command -v go >/dev/null 2>&1; then
    echo "go toolchain not found; required for mash-test" >&2
    exit 1
fi
if [[ ! -d "$MASH_GO_DIR" ]]; then
    echo "mash-go checkout missing: $MASH_GO_DIR" >&2
    exit 1
fi

if [[ -z "$DISCRIMINATOR" ]]; then
    # Same offset-by-PID trick the pytest fixture uses; keeps concurrent
    # dev shells from colliding on MASH-NNNN.
    DISCRIMINATOR=$(( ($$ * 7919) % 4096 ))
fi

# Pick free ports up front so we can pin the operational target precisely
# even if mDNS browsing on the harness side is flaky.
OP_PORT=$($PYTHON -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
COMM_PORT=$($PYTHON -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')

STATE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mash-py-phase22.XXXXXX")"
trap 'rm -rf "$STATE_DIR"' EXIT

DEVICE_LOG="$STATE_DIR/device.log"

echo "[run_conformance] discriminator=$DISCRIMINATOR  ports=op/$OP_PORT,comm/$COMM_PORT"
echo "[run_conformance] state-dir=$STATE_DIR"
echo "[run_conformance] files=$FILES"
echo "[run_conformance] exclude-tags=$EXCLUDE_TAGS"
[[ -n "$FILTER" ]] && echo "[run_conformance] filter=$FILTER"

# Boot the device. Force unbuffered stdio so the device log doesn't lose
# tail bytes when we SIGTERM at the end of the run.
#
# We MUST ``exec`` into python inside the subshell so that ``$!`` captures
# the python PID rather than the (transient) subshell PID. Without
# ``exec``, bash forks one extra process to run ``cd && python``: the
# subshell ends up as $!, the python becomes its child and is reparented
# to init when the subshell exits, and the trap's
# ``kill -TERM "$DEVICE_PID"`` signals an already-dead subshell — leaving
# the device alive on the LAN with active mDNS advertisements. Past
# session debris (stale ``_mash._tcp`` records that outlive the script)
# always traces back to this exact PID-mismatch. The ``cd`` runs before
# ``exec`` so the python's working directory is correct, and we set
# ``PYTHONUNBUFFERED`` via ``env`` because the ``KEY=val cmd`` shell
# syntax requires a real command, not ``exec``.
( cd "$MASH_PY_ROOT" && exec env PYTHONUNBUFFERED=1 "$PYTHON" -m mash_py.cli.device \
    --discriminator "$DISCRIMINATOR" \
    --setup-code "$SETUP_CODE" \
    --port "$OP_PORT" \
    --commissioning-port "$COMM_PORT" \
    --state-dir "$STATE_DIR" \
    --reset \
    --log-level "$LOG_LEVEL" \
    --zone-name phase22-cli ) >"$DEVICE_LOG" 2>&1 &
DEVICE_PID=$!
# Graceful shutdown discipline: SIGTERM, give the device up to 30s to
# fire mDNS goodbyes via Advertiser.close(), then escalate. Without the
# bounded wait, the trap can return while the device is still mid-
# unregister and orphan its records on the LAN.
shutdown_device() {
    if kill -0 "$DEVICE_PID" 2>/dev/null; then
        kill -TERM "$DEVICE_PID" 2>/dev/null || true
        for _ in $(seq 1 60); do
            kill -0 "$DEVICE_PID" 2>/dev/null || break
            sleep 0.5
        done
        if kill -0 "$DEVICE_PID" 2>/dev/null; then
            kill -KILL "$DEVICE_PID" 2>/dev/null || true
        fi
    fi
    wait "$DEVICE_PID" 2>/dev/null || true
}
trap 'shutdown_device; rm -rf "$STATE_DIR"' EXIT

# Wait for the advertise log line so the runner doesn't dial before the
# listener is bound.
deadline=$(( $(date +%s) + 30 ))
while ! grep -q "advertised _mash-comm._tcp" "$DEVICE_LOG" 2>/dev/null; do
    if ! kill -0 "$DEVICE_PID" 2>/dev/null; then
        echo "[run_conformance] device exited early; log:" >&2
        cat "$DEVICE_LOG" >&2 || true
        exit 1
    fi
    if [[ $(date +%s) -ge $deadline ]]; then
        echo "[run_conformance] timed out waiting for device to advertise" >&2
        cat "$DEVICE_LOG" >&2 || true
        exit 1
    fi
    sleep 0.2
done

echo "[run_conformance] device advertising; running mash-test"

GO_ARGS=(
    -target "127.0.0.1:$OP_PORT"
    -srv-resolve
    -discriminator "$DISCRIMINATOR"
    -mode device
    -setup-code "$SETUP_CODE"
    -files "$FILES"
    -exclude-tags "$EXCLUDE_TAGS"
    -timeout 30s
)
[[ -n "$FILTER" ]] && GO_ARGS+=(-filter "$FILTER")

set +e
( cd "$MASH_GO_DIR" && go run ./cmd/mash-test "${GO_ARGS[@]}" )
GO_RC=$?
set -e

if [[ $GO_RC -ne 0 ]]; then
    echo "[run_conformance] FAILED -- device log follows ($DEVICE_LOG):" >&2
    sed 's/^/[device] /' "$DEVICE_LOG" >&2 || true
fi
echo "[run_conformance] mash-test exit=$GO_RC"
exit $GO_RC
