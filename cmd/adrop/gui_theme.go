//go:build gui

package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// adropTheme mirrors the Android app's Material 3 look (see
// android/.../ui/theme/Theme.kt) so the desktop window feels like the same
// product. The phone is restrained: a light-grey surface with the teal brand
// color used only as an *accent* (text, icons, selection), not as fills — its
// buttons are pale/outlined, not painted. So we override only the accent
// (primary + selection) and the surface tone, and leave buttons, inputs, and
// everything else to Fyne's default theme. Fyne picks the light/dark variant
// from the OS setting, matching the phone's LightColors / DarkColors.
type adropTheme struct{}

var _ fyne.Theme = adropTheme{}

// Brand accent, lifted verbatim from the phone's Theme.kt primary.
var (
	teal = color.NRGBA{R: 0x00, G: 0x68, B: 0x74, A: 0xFF} // light primary #006874
	cyan = color.NRGBA{R: 0x4F, G: 0xD8, B: 0xEB, A: 0xFF} // dark primary  #4FD8EB

	// Surfaces. The phone's home sits on a light grey (M3 surfaceVariant),
	// not pure white; the dark surface is the phone's #191C1D.
	lightSurface = color.NRGBA{R: 0xEE, G: 0xF1, B: 0xF2, A: 0xFF}
	darkSurface  = color.NRGBA{R: 0x19, G: 0x1C, B: 0x1D, A: 0xFF}

	// A faint teal wash for selection highlights (M3 secondaryContainer feel).
	tealSelection = color.NRGBA{R: 0x97, G: 0xF0, B: 0xFF, A: 0x66}
)

func (adropTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	dark := v == theme.VariantDark
	switch name {
	case theme.ColorNamePrimary:
		if dark {
			return cyan
		}
		return teal
	case theme.ColorNameSelection:
		return tealSelection
	case theme.ColorNameBackground:
		if dark {
			return darkSurface
		}
		return lightSurface
	}
	return theme.DefaultTheme().Color(name, v)
}

func (adropTheme) Font(s fyne.TextStyle) fyne.Resource { return theme.DefaultTheme().Font(s) }
func (adropTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}
func (adropTheme) Size(n fyne.ThemeSizeName) float32 {
	// A large input/selection radius gives buttons and the peer dropdown the
	// phone's Material 3 "pill" shape instead of Fyne's gently-rounded default.
	switch n {
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 18
	}
	return theme.DefaultTheme().Size(n)
}
