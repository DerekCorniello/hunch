#!/usr/bin/env bash
set -euo pipefail

echo "=== Hunch E2E Integration Test ==="

# Build hunch.
echo "Building..."
cd "$(dirname "$0")/.."
go build -o /tmp/hunch-e2e .

export HUNCH_BIN=/tmp/hunch-e2e
export HUNCH_SOCKET=/tmp/hunch-e2e.sock
export HUNCH_DB_PATH=/tmp/hunch-e2e.db

# Start from a clean slate: stop any daemon left over from a previous run and
# remove its database, so the test is hermetic and repeatable.
"$HUNCH_BIN" daemon stop >/dev/null 2>&1 || true
rm -f /tmp/hunch-e2e.db /tmp/hunch-e2e.db-wal /tmp/hunch-e2e.db-shm /tmp/hunch-e2e.sock

# Always tear the daemon down on exit, even if an assertion fails.
cleanup() {
    "$HUNCH_BIN" daemon stop >/dev/null 2>&1 || true
    rm -f /tmp/hunch-e2e /tmp/hunch-e2e.db /tmp/hunch-e2e.db-wal /tmp/hunch-e2e.db-shm /tmp/hunch-e2e.sock
}
trap cleanup EXIT

# Start daemon.
echo "Starting daemon..."
"$HUNCH_BIN" daemon start
sleep 1

# Record some commands (daemon normalizes raw commands).
echo "Recording transitions..."
"$HUNCH_BIN" client record --state ",git add ." --next "git commit -m init"
"$HUNCH_BIN" client record --state "git add .,git commit -m msg" --next "git push origin main"
"$HUNCH_BIN" client record --state ",git add ." --next "git commit -m init"
"$HUNCH_BIN" client record --state ",git add ." --next "git commit -m init"

# Predict. State is passed as raw commands; the daemon normalizes it the same
# way it normalized the recorded transitions, so the keys line up.
echo "Predicting..."
result=$("$HUNCH_BIN" client predict --state ",git add ." --limit 1 --template)
if [[ "$result" != "git commit FLAG STR" ]]; then
    echo "FAIL: expected 'git commit FLAG STR', got '$result'"
    exit 1
fi
echo "Prediction OK: $result"

# Normalize.
echo "Testing normalize..."
result=$("$HUNCH_BIN" client normalize --cmd "sudo git push origin main")
if [[ "$result" != "git push STR" ]]; then
    echo "FAIL: normalize expected 'git push STR', got '$result'"
    exit 1
fi
echo "Normalize OK: $result"

# Stats.
echo "Testing stats..."
result=$("$HUNCH_BIN" client stats 2>&1)
if ! echo "$result" | grep -q '"size"'; then
    echo "FAIL: stats missing size field"
    exit 1
fi
echo "Stats OK"

# Config.
echo "Testing config..."
"$HUNCH_BIN" client config >/dev/null

# Export.
echo "Testing export..."
result=$("$HUNCH_BIN" client export 2>&1)
if ! echo "$result" | grep -q '"next"'; then
    echo "FAIL: export missing next field"
    exit 1
fi
echo "Export OK"

# Reset.
echo "Testing reset..."
"$HUNCH_BIN" client reset
result=$("$HUNCH_BIN" client predict --state ",git add ." --limit 1 --template)
if [[ -n "$result" ]]; then
    echo "FAIL: predict after reset should be empty, got '$result'"
    exit 1
fi
echo "Reset OK"

# Daemon teardown and file cleanup happen in the EXIT trap.
echo "=== All tests passed ==="
