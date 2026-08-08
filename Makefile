BINARY := adrop
# The resident daemon runs from its own CGO-free binary so the long-lived
# service never depends on the X11/GL libraries the GUI links (SPEC_GUI.md §3.1).
DAEMON_BINARY := adrop-daemon
PREFIX ?= $(HOME)/.local
BINDIR := $(PREFIX)/bin
UNITDIR := $(HOME)/.config/systemd/user

DOLPHIN_DIR := $(HOME)/.local/share/kio/servicemenus
APPS_DIR := $(HOME)/.local/share/applications

.PHONY: all build build-daemon build-headless test test-gui vet vet-gui race install \
        install-headless uninstall clean dolphin-install dolphin-uninstall \
        gui-install gui-uninstall

all: build

# The default build includes the GUI, so a bare `adrop` opens the drop window.
# Fyne needs CGO and a C toolchain plus the X11/GL dev headers (libgl/mesa,
# libxcursor, libxrandr, libxinerama, libxi, libxxf86vm).
build:
	CGO_ENABLED=1 go build -tags gui -o $(BINARY) ./cmd/adrop

# build-daemon produces the static, CGO-free binary the systemd unit runs. Same
# source, no `gui` tag: the resident service keeps working across a mesa/xorg
# change, and on a Wayland-only box with no X11 client libraries installed.
build-daemon:
	CGO_ENABLED=0 go build -o $(DAEMON_BINARY) ./cmd/adrop

# build-headless produces the same CGO-free binary under the plain `adrop` name,
# for machines with no display stack. It has no window, so a bare `adrop` prints
# usage there.
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

# The GUI's own tests (Fyne's test driver, no display needed) are behind the
# same tag as the code they cover, so `make test` alone doesn't compile them.
test-gui:
	CGO_ENABLED=1 go test -tags gui ./...

race:
	go test -race ./...

# Two binaries: the GUI/CLI `adrop` the user runs, and the CGO-free
# `adrop-daemon` the systemd unit runs.
install: build build-daemon
	install -Dm755 $(BINARY) $(BINDIR)/$(BINARY)
	install -Dm755 $(DAEMON_BINARY) $(BINDIR)/$(DAEMON_BINARY)
	install -Dm644 packaging/systemd/adrop.service $(UNITDIR)/adrop.service
	@echo "Installed. Enable with:"
	@echo "  systemctl --user daemon-reload"
	@echo "  systemctl --user enable --now adrop"
	@echo "(upgrading: the unit now runs $(DAEMON_BINARY), so restart it too)"

# The headless install has no GUI binary to keep separate, so the one CGO-free
# build is installed under both names — the unit's ExecStart is the same either
# way.
install-headless: build-headless
	install -Dm755 $(BINARY) $(BINDIR)/$(BINARY)
	install -Dm755 $(BINARY) $(BINDIR)/$(DAEMON_BINARY)
	install -Dm644 packaging/systemd/adrop.service $(UNITDIR)/adrop.service
	@echo "Installed headless binary. Enable the daemon with:"
	@echo "  systemctl --user daemon-reload"
	@echo "  systemctl --user enable --now adrop"

uninstall:
	systemctl --user disable --now adrop 2>/dev/null || true
	rm -f $(BINDIR)/$(BINARY) $(BINDIR)/$(DAEMON_BINARY) $(UNITDIR)/adrop.service

clean:
	rm -f $(BINARY) $(DAEMON_BINARY)

# Both .desktop installs rewrite Exec= to an absolute path: the desktop session's
# PATH does not necessarily include $(BINDIR) (systemd --user starts with a bare
# PATH). The files in packaging/ keep the portable bare `Exec=adrop ...` form.
# They deliberately do NOT depend on `install`: copying one .desktop file must
# not rebuild and overwrite the installed binary (which would undo a deliberate
# `make install-headless`), and the Dolphin menu is pure CLI, installable on a
# box without Fyne's build dependencies. They only warn if the path they bake in
# isn't there yet.
dolphin-install:
	@install -d $(DOLPHIN_DIR)
	sed 's|^Exec=$(BINARY)\b|Exec=$(BINDIR)/$(BINARY)|' packaging/dolphin/adrop.desktop \
	  > $(DOLPHIN_DIR)/adrop.desktop
	@chmod 644 $(DOLPHIN_DIR)/adrop.desktop
	@test -x $(BINDIR)/$(BINARY) || \
	  echo "note: $(BINDIR)/$(BINARY) does not exist yet — run 'make install'"
	@echo "Installed. Restart Dolphin to activate the context menu entry."

dolphin-uninstall:
	rm -f $(DOLPHIN_DIR)/adrop.desktop

gui-install:
	@install -d $(APPS_DIR)
	sed 's|^Exec=$(BINARY)\b|Exec=$(BINDIR)/$(BINARY)|' packaging/desktop/adrop-gui.desktop \
	  > $(APPS_DIR)/adrop-gui.desktop
	@chmod 644 $(APPS_DIR)/adrop-gui.desktop
	@test -x $(BINDIR)/$(BINARY) || \
	  echo "note: $(BINDIR)/$(BINARY) does not exist yet — run 'make install'"
	@echo "Installed app launcher pointing at $(BINDIR)/$(BINARY)."

gui-uninstall:
	rm -f $(APPS_DIR)/adrop-gui.desktop
