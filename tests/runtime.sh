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

CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -gcflags='all=-l' -ldflags="-s -w -buildid= -X main.version=${VERSION}" \
    -o /usr/bin/flowgate .
go test -c -o /tmp/flowgate-go.test .
flowgate version | grep -Fx "Flowgate ${VERSION}"
/src/entrypoint.sh >/tmp/flowgate-runtime.log 2>&1 &
SUPERVISOR_PID=$!
wait_active

FLOWGATE_INTEGRATION=1 /tmp/flowgate-go.test -test.run \
    '^(TestValidateAngieCandidateDoesNotTouchProduction|TestRestoreRuntimeWithNoInitSupervisor)$' -test.v
angie -t
blocky --config /etc/blocky/config.yml validate

flowgate add race-a.example.com >/tmp/race-a.log 2>&1 & p1=$!
flowgate add race-b.example.com >/tmp/race-b.log 2>&1 & p2=$!
wait "$p1"
wait "$p2"
grep -q 'race-a.example.com:' /etc/flowgate/flowgate.yaml
grep -q 'race-b.example.com:' /etc/flowgate/flowgate.yaml
wait_active

flowgate dns dns.example.com
if grep -Eq '^[[:space:]]+https: 8443$' /etc/blocky/config.yml; then
    echo 'Blocky HTTPS enabled before a certificate exists' >&2
    exit 1
fi
mkdir -p /var/lib/angie/acme/acme_dns_example_com
cp /etc/ssl/certs/ssl-cert-snakeoil.pem /var/lib/angie/acme/acme_dns_example_com/certificate.pem
cp /etc/ssl/private/ssl-cert-snakeoil.key /var/lib/angie/acme/acme_dns_example_com/private.key
flowgate sync
wait_active
grep -Eq '^[[:space:]]+https: 8443$' /etc/blocky/config.yml
grep -Eq '^[[:space:]]+tls: 853$' /etc/blocky/config.yml
su-exec blocky test -r /var/lib/angie/acme/acme_dns_example_com/certificate.pem
su-exec blocky test -r /var/lib/angie/acme/acme_dns_example_com/private.key

flowgate add geolocation-!cn
wait_active
angie -t
blocky --config /etc/blocky/config.yml validate
rules=$(wc -l < /etc/blocky/flowgate.list)
[ "$rules" -gt 20000 ]
printf 'runtime integration: PASS (%s rules)\n' "$rules"
