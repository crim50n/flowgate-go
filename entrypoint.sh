#!/bin/sh
set -eu

DNS_PID=""
ANGIE_PID=""
SHUTDOWN=0

start_blocky() {
    su-exec blocky blocky --config /etc/blocky/config.yml &
    DNS_PID=$!
}

start_angie() {
    angie -g 'daemon off;' &
    ANGIE_PID=$!
}

shutdown() {
    SHUTDOWN=1
    trap - INT TERM EXIT
    [ -n "$ANGIE_PID" ] && kill -TERM "$ANGIE_PID" 2>/dev/null || true
    [ -n "$DNS_PID" ] && kill -TERM "$DNS_PID" 2>/dev/null || true
    [ -n "$ANGIE_PID" ] && wait "$ANGIE_PID" 2>/dev/null || true
    [ -n "$DNS_PID" ] && wait "$DNS_PID" 2>/dev/null || true
}
trap shutdown INT TERM EXIT

flowgate init
start_blocky
start_angie
while [ "$SHUTDOWN" -eq 0 ]; do
    if ! kill -0 "$DNS_PID" 2>/dev/null; then
        wait "$DNS_PID" 2>/dev/null || true
        echo "[Flowgate] Restarting Blocky"
        start_blocky
    fi

    if ! kill -0 "$ANGIE_PID" 2>/dev/null; then
        wait "$ANGIE_PID" 2>/dev/null || true
        echo "[Flowgate] Restarting Angie"
        start_angie
    fi

    sleep 1
done
