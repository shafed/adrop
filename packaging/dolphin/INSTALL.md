# KDE Dolphin "Send via adrop" Service Menu

## Manual install

```sh
cp adrop.desktop ~/.local/share/kio/servicemenus/
```

Restart Dolphin (or log out and back in) for the entry to appear.

## Via Makefile

```sh
make dolphin-install
```

## Uninstall

```sh
make dolphin-uninstall
# or manually:
rm ~/.local/share/kio/servicemenus/adrop.desktop
```
