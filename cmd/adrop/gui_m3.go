//go:build gui

package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The Material 3 pieces the phone is built from that Fyne has no equivalent
// for: the three button styles, the card/selectable surface, and the thin
// linear progress indicator. Colors are resolved from adropTheme on every
// Refresh, so a light/dark switch repaints them.

func m3Color(n fyne.ThemeColorName) color.Color { return theme.Color(n) }

// ----- text -----

// m3Text is a wrapping line of text in one M3 type style and color role.
func m3Text(text string, size fyne.ThemeSizeName, c fyne.ThemeColorName, medium bool) *widget.RichText {
	seg := &widget.TextSegment{Text: text, Style: widget.RichTextStyle{
		ColorName: c,
		SizeName:  size,
		TextStyle: fyne.TextStyle{Bold: medium},
		Inline:    true,
	}}
	t := widget.NewRichText(seg)
	t.Wrapping = fyne.TextWrapWord
	return t
}

// setM3Text replaces the text of an m3Text, keeping its style.
func setM3Text(t *widget.RichText, text string) {
	t.Segments[0].(*widget.TextSegment).Text = text
	t.Refresh()
}

// ----- buttons -----

type m3ButtonKind int

const (
	m3Filled   m3ButtonKind = iota // Button: the one action a section exists for
	m3Tonal                        // FilledTonalButton
	m3Outlined                     // OutlinedButton
)

// m3Button is a full-width, 40dp, pill-shaped M3 button.
type m3Button struct {
	widget.BaseWidget
	Text     string
	Icon     fyne.Resource
	OnTapped func()

	kind     m3ButtonKind
	disabled bool
	hovered  bool
	focused  bool
}

func newM3Button(kind m3ButtonKind, text string, icon fyne.Resource, tapped func()) *m3Button {
	b := &m3Button{Text: text, Icon: icon, OnTapped: tapped, kind: kind}
	b.ExtendBaseWidget(b)
	return b
}

func (b *m3Button) SetText(s string) { b.Text = s; b.Refresh() }

func (b *m3Button) Tapped(*fyne.PointEvent) {
	if b.disabled || b.OnTapped == nil {
		return
	}
	b.OnTapped()
}

func (b *m3Button) Enable()        { b.disabled = false; b.Refresh() }
func (b *m3Button) Disable()       { b.disabled = true; b.hovered = false; b.Refresh() }
func (b *m3Button) Disabled() bool { return b.disabled }

func (b *m3Button) MouseIn(*desktop.MouseEvent)    { b.hovered = !b.disabled; b.Refresh() }
func (b *m3Button) MouseMoved(*desktop.MouseEvent) {}
func (b *m3Button) MouseOut()                      { b.hovered = false; b.Refresh() }

func (b *m3Button) Cursor() desktop.Cursor {
	if b.disabled {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}

func (b *m3Button) FocusGained() { b.focused = true; b.Refresh() }
func (b *m3Button) FocusLost()   { b.focused = false; b.Refresh() }
func (b *m3Button) TypedRune(r rune) {
	if r == ' ' {
		b.Tapped(nil)
	}
}
func (b *m3Button) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyReturn || k.Name == fyne.KeyEnter {
		b.Tapped(nil)
	}
}

// colors returns the container, content and border colors for the current
// state, per the M3 button spec.
func (b *m3Button) colors() (bg, fg, border color.Color) {
	onSurface := m3Color(m3OnSurface)
	if b.disabled {
		fg = withAlpha(onSurface, 0.38)
		if b.kind == m3Outlined {
			return color.Transparent, fg, withAlpha(onSurface, 0.12)
		}
		return withAlpha(onSurface, 0.12), fg, color.Transparent
	}
	switch b.kind {
	case m3Filled:
		bg, fg, border = m3Color(theme.ColorNamePrimary), m3Color(m3OnPrimary), color.Transparent
	case m3Tonal:
		bg, fg, border = m3Color(m3SecondaryContainer), m3Color(m3OnSecondaryContainer), color.Transparent
	default:
		bg, fg, border = color.Transparent, m3Color(theme.ColorNamePrimary), m3Color(m3Outline)
	}
	if b.focused {
		border = m3Color(theme.ColorNamePrimary)
	}
	return bg, fg, border
}

func (b *m3Button) CreateRenderer() fyne.WidgetRenderer {
	r := &m3ButtonRenderer{
		b:     b,
		bg:    canvas.NewRectangle(color.Transparent),
		state: canvas.NewRectangle(color.Transparent),
		label: canvas.NewText("", nil),
		icon:  canvas.NewImageFromResource(nil),
	}
	r.label.TextStyle = fyne.TextStyle{Bold: true} // labelLarge: 14sp Medium
	r.icon.FillMode = canvas.ImageFillContain
	r.Refresh()
	return r
}

type m3ButtonRenderer struct {
	b     *m3Button
	bg    *canvas.Rectangle
	state *canvas.Rectangle // hover state layer
	label *canvas.Text
	icon  *canvas.Image
}

const (
	m3ButtonHeight = 40
	m3ButtonIcon   = 18
	m3ButtonGap    = 8
)

func (r *m3ButtonRenderer) contentWidth() float32 {
	w := fyne.MeasureText(r.b.Text, theme.Size(theme.SizeNameText), r.label.TextStyle).Width
	if r.b.Icon != nil {
		w += m3ButtonIcon + m3ButtonGap
	}
	return w
}

func (r *m3ButtonRenderer) MinSize() fyne.Size {
	return fyne.NewSize(r.contentWidth()+48, m3ButtonHeight)
}

func (r *m3ButtonRenderer) Layout(s fyne.Size) {
	r.bg.Resize(s)
	r.state.Resize(s)
	x := (s.Width - r.contentWidth()) / 2
	if r.b.Icon != nil {
		r.icon.Move(fyne.NewPos(x, (s.Height-m3ButtonIcon)/2))
		r.icon.Resize(fyne.NewSquareSize(m3ButtonIcon))
		x += m3ButtonIcon + m3ButtonGap
	}
	ts := r.label.MinSize()
	r.label.Move(fyne.NewPos(x, (s.Height-ts.Height)/2))
	r.label.Resize(ts)
}

func (r *m3ButtonRenderer) Refresh() {
	bg, fg, border := r.b.colors()
	radius := float32(m3ButtonHeight / 2)
	r.bg.FillColor, r.bg.StrokeColor, r.bg.CornerRadius = bg, border, radius
	r.bg.StrokeWidth = 0
	if border != color.Transparent {
		r.bg.StrokeWidth = 1
		if r.b.focused {
			r.bg.StrokeWidth = 2
		}
	}
	r.state.CornerRadius = radius
	r.state.FillColor = color.Transparent
	if r.b.hovered {
		r.state.FillColor = withAlpha(fg, 0.08)
	}
	r.label.Text, r.label.Color = r.b.Text, fg
	r.label.TextSize = theme.Size(theme.SizeNameText)
	if r.b.Icon != nil {
		r.icon.Resource = theme.NewColoredResource(r.b.Icon, r.b.contentRole())
		r.icon.Show()
	} else {
		r.icon.Hide()
	}
	r.Layout(r.b.Size())
	canvas.Refresh(r.b)
}

// colorRole names the theme role behind a button's content color, so the icon
// is tinted through the theme and follows a light/dark switch.
func (b *m3Button) contentRole() fyne.ThemeColorName {
	switch {
	case b.disabled:
		return theme.ColorNameDisabled
	case b.kind == m3Filled:
		return m3OnPrimary
	case b.kind == m3Tonal:
		return m3OnSecondaryContainer
	}
	return theme.ColorNamePrimary
}

func (r *m3ButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.state, r.icon, r.label}
}
func (r *m3ButtonRenderer) Destroy() {}

// ----- surfaces: Card and the selectable device row -----

// m3Card is a rounded M3 container around content: a Card when OnTapped is
// nil, the Send screen's selectable device row when it is set.
type m3Card struct {
	widget.BaseWidget
	Fill     fyne.ThemeColorName
	Border   fyne.ThemeColorName // "" for none
	Content  fyne.CanvasObject
	OnTapped func()

	hovered bool
	focused bool
	flat    bool // no corner radius: a full-width band
}

// square drops the corner radius, for a band such as the TopAppBar.
func (s *m3Card) square() *m3Card {
	s.flat = true
	s.Refresh()
	return s
}

func newM3Card(fill, border fyne.ThemeColorName, content fyne.CanvasObject) *m3Card {
	s := &m3Card{Fill: fill, Border: border, Content: content}
	s.ExtendBaseWidget(s)
	return s
}

// SetColors restyles the surface, e.g. a card turning primaryContainer while a
// transfer is running.
func (s *m3Card) SetColors(fill, border fyne.ThemeColorName) {
	s.Fill, s.Border = fill, border
	s.Refresh()
}

func (s *m3Card) Tapped(*fyne.PointEvent) {
	if s.OnTapped != nil {
		s.OnTapped()
	}
}

func (s *m3Card) MouseIn(*desktop.MouseEvent)    { s.hovered = s.OnTapped != nil; s.Refresh() }
func (s *m3Card) MouseMoved(*desktop.MouseEvent) {}
func (s *m3Card) MouseOut()                      { s.hovered = false; s.Refresh() }

func (s *m3Card) Cursor() desktop.Cursor {
	if s.OnTapped == nil {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}

func (s *m3Card) FocusGained() { s.focused = true; s.Refresh() }
func (s *m3Card) FocusLost()   { s.focused = false; s.Refresh() }
func (s *m3Card) TypedRune(r rune) {
	if r == ' ' {
		s.Tapped(nil)
	}
}
func (s *m3Card) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyReturn || k.Name == fyne.KeyEnter {
		s.Tapped(nil)
	}
}

func (s *m3Card) CreateRenderer() fyne.WidgetRenderer {
	r := &m3CardRenderer{
		s:     s,
		bg:    canvas.NewRectangle(color.Transparent),
		state: canvas.NewRectangle(color.Transparent),
	}
	r.Refresh()
	return r
}

type m3CardRenderer struct {
	s     *m3Card
	bg    *canvas.Rectangle
	state *canvas.Rectangle
}

const m3CardRadius = 12

func (r *m3CardRenderer) MinSize() fyne.Size { return r.s.Content.MinSize() }

func (r *m3CardRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.state.Resize(size)
	r.s.Content.Resize(size)
}

func (r *m3CardRenderer) Refresh() {
	radius := float32(m3CardRadius)
	if r.s.flat {
		radius = 0
	}
	r.bg.FillColor = m3Color(r.s.Fill)
	r.bg.CornerRadius = radius
	r.bg.StrokeWidth = 0
	switch {
	case r.s.focused:
		r.bg.StrokeColor, r.bg.StrokeWidth = m3Color(theme.ColorNamePrimary), 2
	case r.s.Border != "":
		r.bg.StrokeColor, r.bg.StrokeWidth = m3Color(r.s.Border), 1
	}
	r.state.CornerRadius = radius
	r.state.FillColor = color.Transparent
	if r.s.hovered {
		r.state.FillColor = withAlpha(m3Color(m3OnSurface), 0.08)
	}
	r.Layout(r.s.Size())
	canvas.Refresh(r.s)
}

func (r *m3CardRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.state, r.s.Content}
}
func (r *m3CardRenderer) Destroy() {}

// m3Inset pads content by pad dp on all sides (16dp is the phone's card
// padding). RichText already carries theme.InnerPadding of its own, so
// text-only rows pass the remainder.
func m3Inset(pad float32, content fyne.CanvasObject) fyne.CanvasObject {
	return container.New(insetLayout(pad), content)
}

type insetLayout float32

func (l insetLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return objs[0].MinSize().AddWidthHeight(2*float32(l), 2*float32(l))
}

func (l insetLayout) Layout(objs []fyne.CanvasObject, s fyne.Size) {
	objs[0].Move(fyne.NewPos(float32(l), float32(l)))
	objs[0].Resize(s.SubtractWidthHeight(2*float32(l), 2*float32(l)))
}

// ----- linear progress -----

// m3Progress is M3's LinearProgressIndicator: a 4dp rounded track with the
// filled part in primary, no percentage text. It carries its own 8dp margin
// above and below, so hiding it takes the spacing away too.
type m3Progress struct {
	widget.BaseWidget
	value float64
	inset float32 // horizontal margin, to line up with text inside a card
}

func newM3Progress() *m3Progress {
	p := &m3Progress{}
	p.ExtendBaseWidget(p)
	return p
}

func (p *m3Progress) SetValue(v float64) {
	p.value = min(max(v, 0), 1)
	p.Refresh()
}

func (p *m3Progress) CreateRenderer() fyne.WidgetRenderer {
	r := &m3ProgressRenderer{p: p, track: canvas.NewRectangle(nil), bar: canvas.NewRectangle(nil)}
	r.Refresh()
	return r
}

type m3ProgressRenderer struct {
	p          *m3Progress
	track, bar *canvas.Rectangle
}

const m3ProgressHeight = 4

func (r *m3ProgressRenderer) MinSize() fyne.Size { return fyne.NewSize(40, m3ProgressHeight+16) }

func (r *m3ProgressRenderer) Layout(s fyne.Size) {
	x, y := r.p.inset, (s.Height-m3ProgressHeight)/2
	w := s.Width - 2*x
	r.track.Move(fyne.NewPos(x, y))
	r.track.Resize(fyne.NewSize(w, m3ProgressHeight))
	r.bar.Move(fyne.NewPos(x, y))
	r.bar.Resize(fyne.NewSize(w*float32(r.p.value), m3ProgressHeight))
}

func (r *m3ProgressRenderer) Refresh() {
	r.track.FillColor = m3Color(m3SurfaceVariant)
	r.bar.FillColor = m3Color(theme.ColorNamePrimary)
	r.track.CornerRadius, r.bar.CornerRadius = m3ProgressHeight/2, m3ProgressHeight/2
	r.Layout(r.p.Size())
	canvas.Refresh(r.p)
}

func (r *m3ProgressRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.bar}
}
func (r *m3ProgressRenderer) Destroy() {}

// vgap is a fixed vertical gap, for the phone's spacedBy()/Spacer() rhythm.
func vgap(h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(0, h))
	return r
}
