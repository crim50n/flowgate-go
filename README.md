# Flowgate

Flowgate manages an Angie + Blocky gateway for Smart DNS, TLS/SNI passthrough, and HTTPS reverse proxy services.

## Passthrough rules

`flowgate add` accepts either a domain or a GeoSite selector from V2Fly Domain List Community:

```bash
flowgate add example.com
flowgate add category-dev
flowgate add github
```

Flowgate follows the original project's suffix-domain Smart DNS model: configured domains match both the domain itself and its subdomains. For GeoSite selectors, `domain` and `full` entries are normalized to that same suffix behavior. Blocky's `customDNS.mapping` returns the Flowgate IPv4 address for A queries and, with `filterUnmappedTypes: true`, returns NOERROR with an empty Answer for AAAA, HTTPS, SVCB, and other unmapped record types.

Blocky cannot represent DLC `regexp` or `keyword` rules in `customDNS.mapping`. Flowgate reports how many such rules are skipped for DNS interception; Angie can still use them for SNI matching if traffic reaches Flowgate directly.

```text
Client -> Blocky -> Flowgate IP -> Angie :443 -> origin:443
```

TLS is not terminated for passthrough traffic; the client receives the origin certificate.

## Domain list database

Flowgate stores Domain List Community data at:

```text
/var/lib/flowgate/dlc.dat
/var/lib/flowgate/dlc.dat.sha256sum
```

`dlc.dat` is optional until a GeoSite selector is used. A fresh `flowgate init` with only explicit domains does not download it. `flowgate add <GEOSITE>` downloads the database on first use; `flowgate sync` also restores a missing database when the current configuration already contains GeoSite selectors.

`flowgate update` explicitly fetches the current upstream release, verifies the published SHA-256 checksum, validates the database, and replaces the local copy atomically. It never refreshes an existing database implicitly during a normal sync.

## Reverse proxy

`flowgate service app.example.net 8080` configures Angie to terminate TLS and proxy the request to the backend:

```text
Client -> authoritative DNS -> Flowgate IP -> Angie -> backend
```

Service domains are not written to Blocky. Point your own domain to Flowgate in authoritative DNS, typically with a wildcard record such as `*.example.net A <FLOWGATE_IP>`.

## CLI

```text
flowgate init
flowgate doctor [-v|--verbose]
flowgate add DOMAIN|GEOSITE [DOMAIN|GEOSITE...]
flowgate service DOMAIN PORT [--ip IP]
flowgate dns set DOMAIN [--replace]
flowgate check DOMAIN
flowgate explain DOMAIN
flowgate remove DOMAIN|GEOSITE [DOMAIN|GEOSITE...]
flowgate status
flowgate update
flowgate sync
flowgate version
```

`add`, `service`, `dns`, and `remove` run synchronization immediately. Blocky is restarted only when its generated configuration or TLS certificate changes, or when the service is not running; Angie keeps its normal validation and reload behavior.

### DNS endpoint configuration

`flowgate dns set dns.example.net` records the primary DNS hostname and creates the Angie reverse-proxy route to Blocky's HTTPS endpoint. This is a configuration command. The legacy `flowgate dns DOMAIN` spelling remains supported and prints a warning directing diagnostic use to `flowgate check`.

New routes created by this command carry `managed_by: dns` in their domain entry. Changing the primary hostname removes previous DNS-managed routes. Existing user-managed services, including unmarked routes from older releases, are preserved; remove an obsolete legacy alias explicitly with `flowgate remove OLD_DOMAIN`. Selecting an existing service already pointing to local Blocky does not take ownership of it. Replacing a different proxy or service route requires `--replace`. A primary DNS backend cannot be repurposed with `flowgate service` until another primary hostname is selected or the old endpoint is removed.

The hostname must resolve to the Flowgate server through authoritative DNS. On first setup, Angie must obtain the ACME certificate before Blocky can enable its own `https: 8443` and `tls: 853` listeners; the next `flowgate sync` after the certificate appears enables those listeners. Legacy automatic hostname inference applies only when there is exactly one `dns.` or `doh.` service pointing to local Blocky; otherwise configure the primary hostname explicitly.

### Read-only domain diagnostics

```bash
flowgate check tbank.ru
flowgate explain paypal.com
```

Both commands inspect the existing configuration and local DLC database. They show whether Blocky rewrites DNS, which Angie route would apply, and the matching explicit domains or configured GeoSite selectors. The DLC database contains expanded rules, so the output names configured selectors rather than inventing an include hierarchy. Regex and keyword rules are identified as SNI-only; PCRE expressions that the diagnostic cannot evaluate are reported as unknown.

These commands do not initialize configuration, acquire a write lock, download data, query DNS, or restart services. A missing database is an error instead of an apparent direct route. The output describes configuration, not the applied runtime, the client's VPN routing, or application connectivity.

### Conflicting routes

Synchronization rejects a proxy or GeoSite suffix that also covers a configured service. For example, proxying `example.com` while serving `app.example.com` would otherwise make Blocky rewrite a service domain that should use authoritative DNS. Remove the overlapping domain or selector before retrying. Service domains remain absent from Blocky's mappings.

Domain names are matched case insensitively, and ambiguous duplicate names are rejected. Service hostnames that collapse to the same legacy ACME client name, such as `app-example.com` and `app.example.com`, are rejected before applying configuration. Existing certificate directory names are preserved; choose noncolliding service hostnames. Proxy and backend IP addresses must use IPv4 notation.

## Configuration

The main configuration file is `/etc/flowgate/flowgate.yaml`:

```yaml
settings:
  proxy_ip: 203.0.113.10
domains:
  example.com: {type: proxy}
  app.example.net:
    type: service
    ip: 127.0.0.1
    port: 8080
geosites:
  - category-dev
```

GeoSite selectors are stored as selectors, not expanded into the YAML file. They are resolved from the local `dlc.dat` during synchronization.

## Versioning and releases

Release versions come from Git tags in the form `vX.Y.Z`. Untagged builds report `dev` from `flowgate version`. For a local release-like build before tagging, pass the version explicitly:

```bash
make VERSION=1.1.0 build
./flowgate version
```

Release notes are stored separately in `release-notes/<version>.md`. The current release notes are `release-notes/1.1.0.md`. A tagged release requires the matching notes file; the workflow builds versioned amd64/arm64 binaries and uses that file as the GitHub Release description.

## Build and tests

```bash
make test
make build
```

`make build` produces a static Linux binary with `CGO_ENABLED=0`, `-trimpath`, disabled inlining, and stripped symbol/debug data. For the smallest distributable executable, install UPX and run `make release`.

For repeated local validation, build the reusable test environment once:

```bash
make test-image
```

It contains the pinned Go toolchain, race build dependencies, Angie, Blocky, UPX, `staticcheck`, and `govulncheck`. Source code is mounted at runtime, so ordinary source changes do not rebuild the image. Persistent Docker volumes keep the Go module and build caches.

```bash
make test-fast         # unit + race + vet + staticcheck + govulncheck
make test-integration  # Angie + Blocky runtime, rollback, concurrency, TLS and GeoSite
```

The GitHub Actions workflow uses Go 1.26.10 and gates release builds on unit tests, the race detector, `go vet`, `staticcheck`, `govulncheck`, and Docker runtime integration. Docker builds and the reusable test image use the same Go version. The runtime and test environments retain Alpine 3.22 with Angie 1.12.1-r0 and Blocky 0.28.2; the test image copies the pinned Go toolchain from the official Go image. Release binaries are compressed with UPX 5.1.1 and accompanied by SHA-256 checksums.

## Docker

```bash
docker build -t flowgate .
docker run -d --name flowgate \
  -p 53:53/udp -p 53:53/tcp \
  -p 80:80 -p 443:443 -p 853:853 \
  -v flowgate_config:/etc/flowgate \
  -v flowgate_data:/var/lib/flowgate \
  -v angie_state:/var/lib/angie \
  -e PROXY_IP=auto \
  flowgate
```
