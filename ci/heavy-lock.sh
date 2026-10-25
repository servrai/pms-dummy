#!/bin/sh
# servr-dev2 runs every repo's heavy jobs (Gradle builds, the e2e emulator, docker builds) on one host; stacked, they
# starve it (the emulator's main loop hangs and the device is lost). This holds the host-wide heavy-job lock for the
# rest of the job (servr-ai-infra docs/runbooks/dev2-heavy-lock.md):
#
#   ci/heavy-lock.sh acquire   # first step: waits (bounded) until the lock is ours
#   ci/heavy-lock.sh release   # last step, `if: always()`
#
# The lock file is the host's /srv/servr-locks/dev2-heavy.lock, bind-mounted into the job container at /servr-locks.
# A background process keeps it locked across steps; the kernel drops the lock when that process ends, so a cancelled
# or crashed job cannot wedge the queue.
set -eu

LOCK="${HEAVY_LOCK:-/servr-locks/dev2-heavy.lock}"
WAIT_SECONDS="${HEAVY_LOCK_WAIT_SECONDS:-3300}"
STATE="${RUNNER_TEMP:?}/heavy-lock"

case "${1:-}" in
acquire)
    if [ ! -w "$LOCK" ]; then
        echo "::error::$LOCK is not mounted (container options: -v /srv/servr-locks:/servr-locks)"
        exit 1
    fi
    rm -f "$STATE.held"
    # The holder: locks fd 9, says so, then sleeps with fd 9 open until released (2 h hard backstop).
    (
        flock -w "$WAIT_SECONDS" 9 || exit 1
        touch "$STATE.held"
        exec sleep 7200
    ) 9>"$LOCK" &
    echo $! >"$STATE.pid"
    waited=0
    while [ ! -f "$STATE.held" ]; do
        if ! kill -0 "$(cat "$STATE.pid")" 2>/dev/null; then
            echo "::error::servr-dev2 heavy-job lock not granted within $((WAIT_SECONDS / 60)) min"
            exit 1
        fi
        sleep 5
        waited=$((waited + 5))
        [ $((waited % 300)) -eq 0 ] && echo "waiting for the servr-dev2 heavy-job lock (${waited}s)"
    done
    echo "servr-dev2 heavy-job lock held (waited ${waited}s)"
    ;;
release)
    if [ -f "$STATE.pid" ]; then
        kill "$(cat "$STATE.pid")" 2>/dev/null || true
        rm -f "$STATE.pid" "$STATE.held"
        echo "servr-dev2 heavy-job lock released"
    fi
    ;;
*)
    echo "usage: $0 acquire|release" >&2
    exit 2
    ;;
esac
