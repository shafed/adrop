BINARY := adrop
PREFIX ?= $(HOME)/.local
BINDIR := $(PREFIX)/bin
UNITDIR := $(HOME)/.config/systemd/user

DOLPHIN_DIR := $(HOME)/.local/share/kio/servicemenus
APPS_DIR := $(HOME)/.local/share/applications

.PHONY: all build build-gui test vet race install uninstall clean \
        dolphin-install dolphin-uninstall gui-install gui-uninstall

all: build

build:
	CGO_ENABLED=0 go build -o $(BINARY) ./cmd/adrop

# build-gui produces a GUI-enabled binary. Fyne needs CGO and a C toolchain
# plus the X11/GL dev headers (libgl/mesa, libxcursor, libxrandr, libxinerama,
# libxi, libxxf86vm). The default `build` target stays CGO-free and GUI-free.
build-gui:
	CGO_ENABLED=1 go build -tags gui -o $(BINARY) ./cmd/adrop

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

dolphin-install:
	install -Dm644 packaging/dolphin/adrop.desktop $(DOLPHIN_DIR)/adrop.desktop
	@echo "Installed. Restart Dolphin to activate the context menu entry."

dolphin-uninstall:
	rm -f $(DOLPHIN_DIR)/adrop.desktop

gui-install:
	install -Dm644 packaging/desktop/adrop-gui.desktop $(APPS_DIR)/adrop-gui.desktop
	@echo "Installed app launcher. Build the GUI binary with: make build-gui install"

gui-uninstall:
	rm -f $(APPS_DIR)/adrop-gui.desktop
