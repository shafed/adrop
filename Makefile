BINARY := adrop
PREFIX ?= $(HOME)/.local
BINDIR := $(PREFIX)/bin
UNITDIR := $(HOME)/.config/systemd/user

.PHONY: all build test vet race install uninstall clean

all: build

build:
	CGO_ENABLED=0 go build -o $(BINARY) ./cmd/adrop

test:
	go test ./...

vet:
	go vet ./...

race:
	go test -race ./...

install: build
	install -Dm755 $(BINARY) $(BINDIR)/$(BINARY)
	install -Dm644 packaging/systemd/adrop.service $(UNITDIR)/adrop.service
	@echo "Installed. Enable with:"
	@echo "  systemctl --user daemon-reload"
	@echo "  systemctl --user enable --now adrop"

uninstall:
	systemctl --user disable --now adrop 2>/dev/null || true
	rm -f $(BINDIR)/$(BINARY) $(UNITDIR)/adrop.service

clean:
	rm -f $(BINARY)
