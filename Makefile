BINARY := adrop
DAEMON_BINARY := adrop-daemon
RELAY_BINARY := adrop-relay
PREFIX ?= $(HOME)/.local
BINDIR := $(PREFIX)/bin
UNITDIR := $(HOME)/.config/systemd/user

DOLPHIN_DIR := $(HOME)/.local/share/kio/servicemenus
APPS_DIR := $(HOME)/.local/share/applications

.PHONY: all build build-daemon build-relay test vet race install relay-install \
        relay-uninstall uninstall clean dolphin-install dolphin-uninstall \
        core-android app-linux app-android app-install app-uninstall windows

APP_DIR := $(HOME)/.local/lib/adrop-gui
ICON_DIR := $(HOME)/.local/share/icons/hicolor
FLUTTER ?= flutter

all: build

# One static, CGO-free binary: the CLI, and the daemon the systemd unit runs
# (installed a second time as adrop-daemon, the name the unit has always
# used). The window is the Flutter app in app/ (app-linux).
build:
	CGO_ENABLED=0 go build -o $(BINARY) ./cmd/adrop

build-daemon:
	CGO_ENABLED=0 go build -o $(DAEMON_BINARY) ./cmd/adrop

# build-relay produces the FCM wake relay. Optional: it only matters if you use
# FCM wake, and it needs a Firebase service-account key at run time.
build-relay:
	CGO_ENABLED=0 go build -o $(RELAY_BINARY) ./cmd/adrop-relay

test:
	go test ./...

vet:
	go vet ./...

race:
	go test -race ./...

install: build
	install -Dm755 $(BINARY) $(BINDIR)/$(BINARY)
	install -Dm755 $(BINARY) $(BINDIR)/$(DAEMON_BINARY)
	install -Dm644 packaging/systemd/adrop.service $(UNITDIR)/adrop.service
	@echo "Installed. Enable with:"
	@echo "  systemctl --user daemon-reload"
	@echo "  systemctl --user enable --now adrop"

# Windows: the CLI, and the daemon built without a console window (for
# `adrop autostart on`). The window is `flutter build windows` on Windows.
windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/windows/adrop.exe ./cmd/adrop
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags=-H=windowsgui -o dist/windows/adrop-daemon.exe ./cmd/adrop

# The Android app embeds the Go core (core/) through gomobile.
core-android:
	bash scripts/build-core.sh

app-android: core-android
	cd app && $(FLUTTER) build apk --release

# The window: a client of the daemon over its Unix socket, like the CLI.
app-linux:
	cd app && $(FLUTTER) build linux --release

# app-install puts the window bundle in $(APP_DIR), adrop-gui on PATH (a bare
# `adrop` runs it), the icon in the user's icon theme and an app-menu entry.
app-install: app-linux
	rm -rf $(APP_DIR)
	install -d $(APP_DIR)
	cp -r app/build/linux/x64/release/bundle/. $(APP_DIR)/
	install -d $(BINDIR)
	ln -sf $(APP_DIR)/adrop-gui $(BINDIR)/adrop-gui
	install -Dm644 packaging/icons/adrop-256.png $(ICON_DIR)/256x256/apps/adrop.png
	install -Dm644 packaging/icons/adrop-128.png $(ICON_DIR)/128x128/apps/adrop.png
	install -Dm644 packaging/icons/adrop-512.png $(ICON_DIR)/512x512/apps/adrop.png
	@install -d $(APPS_DIR)
	sed 's|^Exec=adrop-gui$$|Exec=$(BINDIR)/adrop-gui|' packaging/desktop/adrop-gui.desktop \
	  > $(APPS_DIR)/adrop-gui.desktop
	@chmod 644 $(APPS_DIR)/adrop-gui.desktop

app-uninstall:
	rm -rf $(APP_DIR) $(BINDIR)/adrop-gui $(APPS_DIR)/adrop-gui.desktop
	rm -f $(ICON_DIR)/256x256/apps/adrop.png $(ICON_DIR)/128x128/apps/adrop.png $(ICON_DIR)/512x512/apps/adrop.png

# relay-install is opt-in: FCM wake needs a Firebase service-account key, which
# not every install has. adrop.service Wants= the relay unit, so once this is
# installed and enabled the relay comes up with the daemon; without it the
# daemon starts anyway and just has no wake fallback.
relay-install: build-relay
	install -Dm755 $(RELAY_BINARY) $(BINDIR)/$(RELAY_BINARY)
	install -Dm644 packaging/systemd/adrop-relay.service $(UNITDIR)/adrop-relay.service
	@echo "Installed the wake relay. It needs a Firebase service-account key at"
	@echo "  ~/.config/adrop/fcm-service-account.json"
	@echo "(the unit is skipped while that file is missing). Then:"
	@echo "  systemctl --user daemon-reload"
	@echo "  systemctl --user enable --now adrop-relay"

relay-uninstall:
	systemctl --user disable --now adrop-relay 2>/dev/null || true
	rm -f $(BINDIR)/$(RELAY_BINARY) $(UNITDIR)/adrop-relay.service

uninstall:
	systemctl --user disable --now adrop 2>/dev/null || true
	rm -f $(BINDIR)/$(BINARY) $(BINDIR)/$(DAEMON_BINARY) $(UNITDIR)/adrop.service

clean:
	rm -f $(BINARY) $(DAEMON_BINARY) $(RELAY_BINARY)

# The .desktop installs rewrite Exec= to an absolute path: the desktop session's
# PATH does not necessarily include $(BINDIR) (systemd --user starts with a bare
# PATH). The files in packaging/ keep the portable bare `Exec=adrop ...` form.
# dolphin-install deliberately does NOT depend on `install`: copying one .desktop file must
# not rebuild and overwrite the installed binary. It only warns if the path it
# bakes in isn't there yet.
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
