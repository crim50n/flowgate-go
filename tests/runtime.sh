#!/bin/sh
set -eu

VERSION=${VERSION:-dev}
SUPERVISOR_PID=""

cleanup() {
    if [ -n "$SUPERVISOR_PID" ]; then
        kill -TERM "$SUPERVISOR_PID" 2>/dev/null || true
        wait "$SUPERVISOR_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT INT TERM

wait_active() {
    for _ in $(seq 1 30); do
        status=$(flowgate status 2>/dev/null || true)
        active=$(printf '%s\n' "$status" | grep -c 'ACTIVE' || true)
        if [ "$active" -ge 2 ] && ! printf '%s\n' "$status" | grep -q 'INACTIVE'; then
            return 0
        fi
        sleep 1
    done
    cat /tmp/flowgate-runtime.log >&2 || true
    return 1
}

wait_new_blocky_pid() {
    old_pid=$1
    for _ in $(seq 1 50); do
        pid=$(pgrep -x blocky | head -n1 || true)
        if [ -n "$pid" ] && [ "$pid" != "$old_pid" ]; then
            printf '%s\n' "$pid"
            return 0
        fi
        sleep 0.1
    done
    return 1
}

wait_stable_blocky_pid() {
    for _ in $(seq 1 30); do
        pid=$(pgrep -x blocky | head -n1 || true)
        if [ -n "$pid" ]; then
            sleep 1.2
            if [ "$(pgrep -x blocky | head -n1 || true)" = "$pid" ]; then
                printf '%s\n' "$pid"
                return 0
            fi
        fi
        sleep 0.1
    done
    return 1
}

CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -gcflags='all=-l' -ldflags="-s -w -buildid= -X main.version=${VERSION}" \
    -o /usr/bin/flowgate .
go test -c -o /tmp/flowgate-go.test .
flowgate version | grep -Fx "Flowgate ${VERSION}"
/src/entrypoint.sh >/tmp/flowgate-runtime.log 2>&1 &
SUPERVISOR_PID=$!
wait_active

FLOWGATE_INTEGRATION=1 /tmp/flowgate-go.test -test.run \
    '^(TestValidateAngieCandidateDoesNotTouchProduction|TestRestoreRuntimeWithNoInitSupervisor|TestRestoreRuntimeSkipsUntouchedBlocky)$' -test.v
angie -t
blocky --config /etc/blocky/config.yml validate

flowgate add race-a.example.com >/tmp/race-a.log 2>&1 & p1=$!
flowgate add race-b.example.com >/tmp/race-b.log 2>&1 & p2=$!
wait "$p1"
wait "$p2"
grep -q 'race-a.example.com:' /etc/flowgate/flowgate.yaml
grep -q 'race-b.example.com:' /etc/flowgate/flowgate.yaml
wait_active

blocky_pid=$(wait_stable_blocky_pid)
for n in 1 2 3 4 5 6; do
    flowgate service "service-${n}.example.com" "$((8000 + n))" >/tmp/service-${n}.log 2>&1
    [ "$(pgrep -x blocky | head -n1)" = "$blocky_pid" ] || {
        echo "Blocky restarted after service-only change" >&2
        exit 1
    }
done
wait_active

flowgate service app.smart-test.invalid 9000 >/tmp/smart-service.log 2>&1
flowgate add example.com >/tmp/smart-add.log 2>&1
wait_stable_blocky_pid >/dev/null
FLOWGATE_INTEGRATION=1 /tmp/flowgate-go.test -test.run \
    '^(TestSmartDNSResponseSemantics|TestSmartDNSTLSPassthrough)$' -test.v

flowgate dns dns.example.com
if grep -Eq '^[[:space:]]+https: 8443$' /etc/blocky/config.yml; then
    echo 'Blocky HTTPS enabled before a certificate exists' >&2
    exit 1
fi
mkdir -p /var/lib/angie/acme/acme_dns_example_com
cp /etc/ssl/certs/ssl-cert-snakeoil.pem /var/lib/angie/acme/acme_dns_example_com/certificate.pem
cp /etc/ssl/private/ssl-cert-snakeoil.key /var/lib/angie/acme/acme_dns_example_com/private.key
blocky_pid=$(wait_stable_blocky_pid)
flowgate sync
wait_new_blocky_pid "$blocky_pid" >/dev/null
wait_active
grep -Eq '^[[:space:]]+https: 8443$' /etc/blocky/config.yml
grep -Eq '^[[:space:]]+tls: 853$' /etc/blocky/config.yml
su-exec blocky test -r /var/lib/angie/acme/acme_dns_example_com/certificate.pem
su-exec blocky test -r /var/lib/angie/acme/acme_dns_example_com/private.key
FLOWGATE_INTEGRATION=1 /tmp/flowgate-go.test -test.run '^TestSmartDNSDoTResponseSemantics$' -test.v

flowgate add category-ai-!cn >/tmp/category-ai.log 2>&1
wait_stable_blocky_pid >/dev/null
FLOWGATE_INTEGRATION=1 FLOWGATE_SMARTDNS_DOMAIN=chatgpt.com /tmp/flowgate-go.test -test.run \
    '^(TestSmartDNSResponseSemantics|TestSmartDNSTLSPassthrough|TestSmartDNSDoTResponseSemantics)$' -test.v

blocky_pid=$(wait_stable_blocky_pid)
for n in 7 8 9; do
    flowgate service "tls-service-${n}.example.com" "$((8000 + n))" >/tmp/tls-service-${n}.log 2>&1
    [ "$(pgrep -x blocky | head -n1)" = "$blocky_pid" ] || {
        echo "Blocky restarted after TLS service-only change" >&2
        exit 1
    }
done

blocky_pid=$(wait_stable_blocky_pid)
FLOWGATE_ROOT=/tmp/renewal-root PROXY_IP=203.0.113.10 flowgate init >/tmp/renewal-init.log 2>&1
cp /tmp/renewal-root/etc/ssl/certs/ssl-cert-snakeoil.pem /var/lib/angie/acme/acme_dns_example_com/certificate.pem
cp /tmp/renewal-root/etc/ssl/private/ssl-cert-snakeoil.key /var/lib/angie/acme/acme_dns_example_com/private.key
flowgate sync >/tmp/renewal-sync.log 2>&1
renewed_pid=$(wait_new_blocky_pid "$blocky_pid") || {
    echo 'Blocky did not restart after certificate renewal' >&2
    exit 1
}
wait_active
flowgate sync >/tmp/renewal-noop.log 2>&1
wait_active
[ "$(pgrep -x blocky | head -n1)" = "$renewed_pid" ] || {
    echo 'Blocky restarted again without a certificate change' >&2
    exit 1
}

flowgate add geolocation-!cn
wait_active
angie -t
blocky --config /etc/blocky/config.yml validate
domains=$(awk '/^    mapping:$/ { inmap=1; next } inmap && /^        [^[:space:]].*: / { count++; next } inmap && !/^        / { exit } END { print count+0 }' /etc/blocky/config.yml)
[ "$domains" -gt 20000 ]
printf 'runtime integration: PASS (%s Smart DNS domains)\n' "$domains"
