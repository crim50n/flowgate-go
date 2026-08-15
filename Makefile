GO ?= go
PREFIX ?= /usr
SYSCONFDIR ?= /etc
DESTDIR ?=

BINARY := flowgate
BINDIR := $(DESTDIR)$(PREFIX)/bin
CONFDIR := $(DESTDIR)$(SYSCONFDIR)/flowgate

UPX ?= upx

.PHONY: all build release test install clean

all: build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -gcflags='all=-l' -ldflags='-s -w -buildid=' -o $(BINARY) .

release: build
	$(UPX) --best --lzma $(BINARY)
	$(UPX) -t $(BINARY)

test:
	$(GO) test ./...
	$(GO) vet ./...

install: build
	install -d -m 755 $(BINDIR) $(CONFDIR)
	install -m 755 $(BINARY) $(BINDIR)/$(BINARY)
	install -m 644 flowgate.yaml.default $(CONFDIR)/flowgate.yaml.default
	@test -f $(CONFDIR)/flowgate.yaml || \
		install -m 644 flowgate.yaml.default $(CONFDIR)/flowgate.yaml

clean:
	rm -f $(BINARY)
