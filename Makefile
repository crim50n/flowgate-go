GO ?= go
PREFIX ?= /usr
SYSCONFDIR ?= /etc
DESTDIR ?=
DOCKER ?= docker

BINARY := flowgate
VERSION ?= $(shell tag=$$(git describe --tags --exact-match --match 'v[0-9]*' 2>/dev/null); if [ -n "$$tag" ]; then printf '%s' "$${tag#v}"; else printf dev; fi)
LDFLAGS := -s -w -buildid= -X main.version=$(VERSION)
BINDIR := $(DESTDIR)$(PREFIX)/bin
CONFDIR := $(DESTDIR)$(SYSCONFDIR)/flowgate

UPX ?= upx
TEST_IMAGE ?= flowgate-test:go1.25.13
TEST_MOD_CACHE ?= flowgate-test-mod-cache
TEST_BUILD_CACHE ?= flowgate-test-build-cache

.PHONY: all build release test test-image test-fast test-integration install clean

all: build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -gcflags='all=-l' -ldflags='$(LDFLAGS)' -o $(BINARY) .

release: build
	$(UPX) --best --lzma $(BINARY)
	$(UPX) -t $(BINARY)

test:
	$(GO) test ./...
	$(GO) vet ./...
test-image:
	$(DOCKER) build -f Dockerfile.test -t $(TEST_IMAGE) .

test-fast:
	$(DOCKER) run --rm \
		-v $(CURDIR):/src \
		-v $(TEST_MOD_CACHE):/go/pkg/mod \
		-v $(TEST_BUILD_CACHE):/root/.cache/go-build \
		-w /src $(TEST_IMAGE) sh -c \
		'go test -count=1 ./... && go test -race -count=1 ./... && go vet ./... && staticcheck ./... && govulncheck ./...'

test-integration:
	$(DOCKER) run --rm \
		-e PROXY_IP=203.0.113.10 \
		-v $(CURDIR):/src \
		-v $(TEST_MOD_CACHE):/go/pkg/mod \
		-v $(TEST_BUILD_CACHE):/root/.cache/go-build \
		-w /src $(TEST_IMAGE) sh -c './tests/runtime.sh'

install: build
	install -d -m 755 $(BINDIR) $(CONFDIR)
	install -m 755 $(BINARY) $(BINDIR)/$(BINARY)
	install -m 644 flowgate.yaml.default $(CONFDIR)/flowgate.yaml.default
	@test -f $(CONFDIR)/flowgate.yaml || \
		install -m 644 flowgate.yaml.default $(CONFDIR)/flowgate.yaml

clean:
	rm -f $(BINARY)
