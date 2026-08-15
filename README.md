# Flowgate

Flowgate manages an Angie + Blocky gateway for Smart DNS, TLS/SNI passthrough, and HTTPS reverse proxy services.

## Passthrough rules

`flowgate add` accepts either a domain or a GeoSite selector from V2Fly Domain List Community:

```bash
flowgate add example.com
flowgate add category-dev
flowgate add github
```

Domains are matched together with their subdomains. GeoSite selectors keep the rule types stored in `dlc.dat` (`domain`, `full`, `keyword`, and `regexp`). Flowgate compiles the same rule set into Blocky and Angie so DNS interception and SNI routing stay aligned.

Passthrough DNS uses Blocky's denylist response with `proxy_ip`: matching A queries return the Flowgate IPv4 address, matching AAAA queries return `::`, and other query types such as HTTPS/SVCB return NXDOMAIN. Blocky's normal CNAME/response matching semantics also apply.

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

`flowgate service app.example.com 8080` configures Angie to terminate TLS and proxy the request to the backend:

```text
Client -> authoritative DNS -> Flowgate IP -> Angie -> backend
```

Service domains are not written to Blocky. Point your own domain to Flowgate in authoritative DNS, typically with a wildcard record such as `*.example.com A <FLOWGATE_IP>`.

## CLI

```text
flowgate init
flowgate doctor [-v|--verbose]
flowgate add DOMAIN|GEOSITE [DOMAIN|GEOSITE...]
flowgate service DOMAIN PORT [--ip IP]
flowgate dns DOMAIN
flowgate remove DOMAIN|GEOSITE [DOMAIN|GEOSITE...]
flowgate status
flowgate update
flowgate sync
flowgate version
```

`add`, `service`, `dns`, and `remove` run synchronization immediately.

`flowgate dns dns.example.com` records the primary DNS hostname and creates the Angie reverse-proxy route to Blocky's HTTPS endpoint. The hostname must resolve to the Flowgate server through authoritative DNS. On first setup, Angie must obtain the ACME certificate before Blocky can enable its own `https: 8443` and `tls: 853` listeners; the next `flowgate sync` after the certificate appears enables those listeners.

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
make VERSION=1.0.0 build
./flowgate version
```

Release notes are stored separately in `release-notes/<version>.md`. The current release notes are `release-notes/1.0.0.md`. A tagged release requires the matching notes file; the workflow builds versioned amd64/arm64 binaries and uses that file as the GitHub Release description.

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

The GitHub Actions workflow uses Go 1.25.13 and gates release builds on unit tests, the race detector, `go vet`, `staticcheck`, `govulncheck`, and Docker runtime integration. Release binaries are compressed with UPX 5.1.1 and accompanied by SHA-256 checksums.

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
