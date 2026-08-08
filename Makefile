BINARY := adrop
PREFIX ?= $(HOME)/.local
BINDIR := $(PREFIX)/bin
UNITDIR := $(HOME)/.config/systemd/user

DOLPHIN_DIR := $(HOME)/.local/share/kio/servicemenus
APPS_DIR := $(HOME)/.local/share/applications

.PHONY: all build build-headless test vet vet-gui race install install-headless \
        uninstall clean dolphin-install dolphin-uninstall gui-install gui-uninstall

all: build

# The default build includes the GUI, so a bare `adrop` opens the drop window.
# Fyne needs CGO and a C toolchain plus the X11/GL dev headers (libgl/mesa,
# libxcursor, libxrandr, libxinerama, libxi, libxxf86vm).
build:
	CGO_ENABLED=1 go build -tags gui -o $(BINARY) ./cmd/adrop

# build-headless produces the static, CGO-free daemon/CLI binary for machines
# with no display stack. It has no window, so a bare `adrop` prints usage there.
build-headless:
	CGO_ENABLED=0 go build -o $(BINARY) ./cmd/adrop

test:
	go test ./...

vet:
	go vet ./...

# `go vet ./...` compiles gui_stub.go, not gui.go, so the GUI needs its own pass.
# Kept separate so `make vet` still runs without the X11/GL dev headers.
vet-gui:
	CGO_ENABLED=1 go vet -tags gui ./...

race:
	go test -race ./...

install: build
	install -Dm755 $(BINARY) $(BINDIR)/$(BINARY)
	install -Dm644 packaging/systemd/adrop.service $(UNITDIR)/adrop.service
	@echo "Installed. Enable with:"
	@echo "  systemctl --user daemon-reload"
	@echo "  systemctl --user enable --now adrop"

install-headless: build-headless
	install -Dm755 $(BINARY) $(BINDIR)/$(BINARY)
	install -Dm644 packaging/systemd/adrop.service $(UNITDIR)/adrop.service
	@echo "Installed headless binary. Enable the daemon with:"
	@echo "  systemctl --user daemon-reload"
	@echo "  systemctl --user enable --now adrop"

uninstall:
	systemctl --user disable --now adrop 2>/dev/null || true
	rm -f $(BINDIR)/$(BINARY) $(UNITDIR)/adrop.service

clean:
	rm -f $(BINARY)

# Both .desktop installs rewrite Exec= to an absolute path: the desktop session's
# PATH does not necessarily include $(BINDIR) (systemd --user starts with a bare
# PATH). The files in packaging/ keep the portable bare `Exec=adrop ...` form.
dolphin-install:
	@install -d $(DOLPHIN_DIR)
	sed 's|^Exec=$(BINARY)\b|Exec=$(BINDIR)/$(BINARY)|' packaging/dolphin/adrop.desktop \
	  > $(DOLPHIN_DIR)/adrop.desktop
	@chmod 644 $(DOLPHIN_DIR)/adrop.desktop
	@echo "Installed. Restart Dolphin to activate the context menu entry."

dolphin-uninstall:
	rm -f $(DOLPHIN_DIR)/adrop.desktop

gui-install:
	@install -d $(APPS_DIR)
	sed 's|^Exec=$(BINARY)\b|Exec=$(BINDIR)/$(BINARY)|' packaging/desktop/adrop-gui.desktop \
	  > $(APPS_DIR)/adrop-gui.desktop
	@chmod 644 $(APPS_DIR)/adrop-gui.desktop
	@echo "Installed app launcher pointing at $(BINDIR)/$(BINARY)."

gui-uninstall:
	rm -f $(APPS_DIR)/adrop-gui.desktop
