# Flowgate

Flowgate manages an Angie + Blocky gateway for Smart DNS, TLS/SNI passthrough, and HTTPS reverse proxy services.

## Traffic modes

### Smart DNS passthrough

`flowgate add example.com` creates a `type: proxy` entry. Flowgate adds it to Blocky and configures Angie to forward TLS by SNI without terminating TLS:

```text
Client -> Blocky -> Flowgate IP -> Angie :443 -> origin:443
```

The client receives the origin server certificate.

### Reverse proxy

`flowgate service app.example.com 8080` creates a `type: service` entry. Flowgate configures Angie to terminate TLS and proxy the request to the configured backend:

```text
Client -> authoritative DNS -> Flowgate IP -> Angie -> backend
```

Service domains are not written to Blocky. Point the domain to Flowgate in authoritative DNS, typically with a wildcard record such as `*.example.com A <FLOWGATE_IP>`.

## CLI

```text
flowgate init
flowgate doctor [-v|--verbose]
flowgate add DOMAIN [DOMAIN...]
flowgate service DOMAIN PORT [--ip IP]
flowgate dns DOMAIN
flowgate remove DOMAIN [DOMAIN...]
flowgate status
flowgate sync
```

`add`, `service`, `dns`, and `remove` run synchronization immediately.

`flowgate dns dns.example.com` records the primary DNS hostname and creates the Angie reverse-proxy route to Blocky's HTTPS endpoint. The hostname must resolve to the Flowgate server through authoritative DNS.

## Configuration

The main configuration file is `/etc/flowgate/flowgate.yaml`:

```yaml
settings:
  proxy_ip: 203.0.113.10
domains:
  openai.com: {type: proxy}
  app.example.com:
    type: service
    ip: 127.0.0.1
    port: 8080
```

## Build

```bash
make test
make build
```

`make build` produces a static Linux binary with `CGO_ENABLED=0`, `-trimpath`, disabled inlining, and stripped symbol/debug data.

For the smallest distributable executable, install UPX and run:

```bash
make release
```

The GitHub Actions workflow `.github/workflows/build.yml` builds Linux amd64 and arm64 binaries with Go 1.25.6, compresses them with UPX 5.1.1 using `--best --lzma`, verifies the packed executable, and uploads the binaries with SHA-256 checksums.

## Docker

```bash
docker build -t flowgate .
docker run -d --name flowgate \
  -p 53:53/udp -p 53:53/tcp \
  -p 80:80 -p 443:443 -p 853:853 \
  -v flowgate_config:/etc/flowgate \
  -v angie_state:/var/lib/angie \
  -e PROXY_IP=auto \
  flowgate
```
