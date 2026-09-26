//go:build gui

package main

import (
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// adropTheme dresses the desktop window in the Android app's Material 3 look:
// the colors it actually shows on the phone, the same type scale
// and Roboto, so the two apps read as one product. Fyne's own color names are
// mapped onto the M3 roles, and the roles Fyne has no name for (containers,
// outline, onSurfaceVariant) are exposed as the m3* names below for the M3
// widgets in gui_m3.go. Fyne picks the light/dark variant from the OS setting,
// matching the phone's LightColors / DarkColors.
type adropTheme struct{}

var _ fyne.Theme = adropTheme{}

// M3 color roles Fyne has no name for.
const (
	m3OnPrimary            fyne.ThemeColorName = "m3OnPrimary"
	m3PrimaryContainer     fyne.ThemeColorName = "m3PrimaryContainer"
	m3OnPrimaryContainer   fyne.ThemeColorName = "m3OnPrimaryContainer"
	m3SecondaryContainer   fyne.ThemeColorName = "m3SecondaryContainer"
	m3OnSecondaryContainer fyne.ThemeColorName = "m3OnSecondaryContainer"
	m3Surface              fyne.ThemeColorName = "m3Surface"
	m3Background           fyne.ThemeColorName = "m3Background"
	m3OnSurface            fyne.ThemeColorName = "m3OnSurface"
	m3SurfaceVariant       fyne.ThemeColorName = "m3SurfaceVariant"
	m3OnSurfaceVariant     fyne.ThemeColorName = "m3OnSurfaceVariant"
	m3Outline              fyne.ThemeColorName = "m3Outline"
	m3OutlineVariant       fyne.ThemeColorName = "m3OutlineVariant"
	m3ErrorContainer       fyne.ThemeColorName = "m3ErrorContainer"
	m3OnErrorContainer     fyne.ThemeColorName = "m3OnErrorContainer"
)

// M3 type scale (sp), as MaterialTheme.typography uses it on the phone.
const (
	m3TitleLarge  fyne.ThemeSizeName = "m3TitleLarge"  // TopAppBar title
	m3TitleMedium fyne.ThemeSizeName = "m3TitleMedium" // section and card titles
	m3TitleSmall  fyne.ThemeSizeName = "m3TitleSmall"  // device name
	m3BodySmall   fyne.ThemeSizeName = "m3BodySmall"   // subtitles, addresses
	m3LabelSmall  fyne.ThemeSizeName = "m3LabelSmall"  // fingerprint
)

func rgb(hex uint32) color.NRGBA {
	return color.NRGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 0xFF}
}

// The light scheme is sampled pixel by pixel from a screenshot of the phone
// app, which runs on Android's dynamic (wallpaper) colors rather than the static
// teal in Theme.kt. Roles the screenshot does not show (primaryContainer,
// error) are the matching tones of the same blue scheme.
var lightScheme = map[fyne.ThemeColorName]color.Color{
	theme.ColorNamePrimary: rgb(0x497AFA), // outlined button text and icons
	m3OnPrimary:            rgb(0xFFFFFF),
	m3PrimaryContainer:     rgb(0xDBE1FF),
	m3OnPrimaryContainer:   rgb(0x00174B),
	m3SecondaryContainer:   rgb(0xDDE2F6), // "Send File or Clipboard" button
	m3OnSecondaryContainer: rgb(0x404656),
	m3Surface:              rgb(0xFCFCFE), // TopAppBar
	m3Background:           rgb(0xF1F1F3), // screen behind the cards
	m3OnSurface:            rgb(0x000000),
	m3SurfaceVariant:       rgb(0xE4E4E6), // "Open to receive" card
	m3OnSurfaceVariant:     rgb(0x404040),
	m3Outline:              rgb(0x79797B),
	m3OutlineVariant:       rgb(0xC6C6C8),
	theme.ColorNameError:   rgb(0xBA1A1A),
	m3ErrorContainer:       rgb(0xFFDAD6),
	m3OnErrorContainer:     rgb(0x410002),
}

// The dark scheme is not sampled (no screenshot yet): it is the dark tones of
// the same blue dynamic scheme.
var darkScheme = map[fyne.ThemeColorName]color.Color{
	theme.ColorNamePrimary: rgb(0xB3C5FF),
	m3OnPrimary:            rgb(0x002B75),
	m3PrimaryContainer:     rgb(0x2B4678),
	m3OnPrimaryContainer:   rgb(0xDBE1FF),
	m3SecondaryContainer:   rgb(0x3F4759),
	m3OnSecondaryContainer: rgb(0xDBE2F9),
	m3Surface:              rgb(0x1B1B1F),
	m3Background:           rgb(0x121316),
	m3OnSurface:            rgb(0xE4E2E6),
	m3SurfaceVariant:       rgb(0x2A2A2D),
	m3OnSurfaceVariant:     rgb(0xC5C6D0),
	m3Outline:              rgb(0x8F9099),
	m3OutlineVariant:       rgb(0x45464F),
	theme.ColorNameError:   rgb(0xFFB4AB),
	m3ErrorContainer:       rgb(0x93000A),
	m3OnErrorContainer:     rgb(0xFFDAD6),
}

// withAlpha returns c at the given opacity — M3's state layers and disabled
// content are the role color at a fixed alpha.
func withAlpha(c color.Color, a float64) color.NRGBA {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	n.A = uint8(float64(n.A) * a)
	return n
}

func (adropTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	s := lightScheme
	if v == theme.VariantDark {
		s = darkScheme
	}
	if c, ok := s[name]; ok {
		return c
	}
	// Fyne's names, mapped onto the M3 roles the phone uses for the same job.
	switch name {
	case theme.ColorNameBackground:
		return s[m3Background]
	case theme.ColorNameMenuBackground, theme.ColorNameHeaderBackground:
		return s[m3Surface]
	case theme.ColorNameOverlayBackground: // dialogs
		return s[m3Surface]
	case theme.ColorNameForeground:
		return s[m3OnSurface]
	case theme.ColorNameForegroundOnPrimary:
		return s[m3OnPrimary]
	case theme.ColorNameButton:
		return s[m3SecondaryContainer]
	case theme.ColorNameInputBackground:
		return withAlpha(s[m3SurfaceVariant], 0.6)
	case theme.ColorNameInputBorder:
		return s[m3Outline]
	case theme.ColorNamePlaceHolder:
		return s[m3OnSurfaceVariant]
	case theme.ColorNameSeparator:
		return s[m3OutlineVariant]
	case theme.ColorNameDisabled:
		return withAlpha(s[m3OnSurface], 0.38)
	case theme.ColorNameDisabledButton:
		return withAlpha(s[m3OnSurface], 0.12)
	case theme.ColorNameHover, theme.ColorNameFocus:
		return withAlpha(s[m3OnSurface], 0.08)
	case theme.ColorNamePressed:
		return withAlpha(s[m3OnSurface], 0.12)
	case theme.ColorNameSelection:
		return withAlpha(s[m3PrimaryContainer], 0.6)
	case theme.ColorNameScrollBar:
		return withAlpha(s[m3OnSurfaceVariant], 0.4)
	}
	return theme.DefaultTheme().Color(name, v)
}

// Roboto is the phone's system face. It is read from the system install
// (Arch: ttf-roboto) rather than bundled; without it the window falls back to
// Fyne's own sans, which is close enough to not look broken.
var (
	robotoOnce    sync.Once
	robotoRegular fyne.Resource
	robotoMedium  fyne.Resource
)

func loadRoboto() {
	robotoOnce.Do(func() {
		const dir = "/usr/share/fonts/TTF/"
		r, err1 := fyne.LoadResourceFromPath(dir + "Roboto-Regular.ttf")
		m, err2 := fyne.LoadResourceFromPath(dir + "Roboto-Medium.ttf")
		if err1 == nil && err2 == nil {
			robotoRegular, robotoMedium = r, m
		}
	})
}

func (adropTheme) Font(s fyne.TextStyle) fyne.Resource {
	loadRoboto()
	if robotoRegular == nil || s.Monospace || s.Symbol || s.Italic {
		return theme.DefaultTheme().Font(s)
	}
	// M3 never uses a true bold: titles and labels are Medium (500).
	if s.Bold {
		return robotoMedium
	}
	return robotoRegular
}

func (adropTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (adropTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case m3TitleLarge:
		return 22
	case m3TitleMedium:
		return 16
	case m3TitleSmall, theme.SizeNameText:
		return 14
	case m3BodySmall:
		return 12
	case m3LabelSmall:
		return 11
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 12 // M3 shapes.medium
	}
	return theme.DefaultTheme().Size(n)
}
