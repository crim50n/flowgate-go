FROM golang:1.25.6-alpine@sha256:98e6cffc31ccc44c7c15d83df1d69891efee8115a5bb7ede2bf30a38af3e3c92 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go flowgate.yaml.default ./
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -gcflags='all=-l' -ldflags='-s -w -buildid=' \
    -o /out/flowgate .

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce
ARG ANGIE_VERSION=1.12.1-r0
ARG BLOCKY_VERSION=v0.28.2

RUN apk add --no-cache ca-certificates libcap su-exec \
    && wget -qO /etc/apk/keys/angie-signing.rsa \
       https://angie.software/keys/angie-signing.rsa \
    && echo "https://download.angie.software/angie/alpine/v$(grep -Eo '[0-9]+\.[0-9]+' /etc/alpine-release)/main" \
       >> /etc/apk/repositories \
    && apk add --no-cache "angie=${ANGIE_VERSION}"
RUN case "$(apk --print-arch)" in \
      x86_64) BLOCKY_ARCH=x86_64; BLOCKY_SHA256=bc93921fb033c25370808639f1f6b62f2356d14cfa00a8e110b514f5001caed0 ;; \
      aarch64) BLOCKY_ARCH=arm64; BLOCKY_SHA256=e7adb5bdff391c8fe88376ed861ee5dd1ae107ea3cb375cb8cc17371846b26b1 ;; \
      *) echo "Unsupported architecture" >&2; exit 1 ;; \
    esac \
    && wget -qO /tmp/blocky.tar.gz \
       "https://github.com/0xERR0R/blocky/releases/download/${BLOCKY_VERSION}/blocky_${BLOCKY_VERSION}_Linux_${BLOCKY_ARCH}.tar.gz" \
    && echo "${BLOCKY_SHA256}  /tmp/blocky.tar.gz" | sha256sum -c - \
    && tar xzf /tmp/blocky.tar.gz -C /usr/bin blocky \
    && rm /tmp/blocky.tar.gz \
    && chmod 0755 /usr/bin/blocky \
    && setcap 'cap_net_bind_service=+ep' /usr/bin/blocky \
    && addgroup -S blocky \
    && adduser -S -D -H -h /var/lib/blocky -s /sbin/nologin -G blocky blocky

RUN rm -f /etc/angie/http.d/default.conf /etc/angie/stream.d/example.conf \
    && mkdir -p /etc/blocky /etc/flowgate /var/lib/flowgate/backups /var/lib/blocky

COPY --from=builder /out/flowgate /usr/bin/flowgate
COPY flowgate.yaml.default /etc/flowgate/flowgate.yaml.default
COPY entrypoint.sh /entrypoint.sh
RUN chmod 0755 /entrypoint.sh
ENV PROXY_IP=auto
ENV DNS_DOMAIN=""

EXPOSE 53/udp 53/tcp 80/tcp 443/tcp 853/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD pgrep angie >/dev/null && pgrep blocky >/dev/null || exit 1

ENTRYPOINT ["/entrypoint.sh"]
