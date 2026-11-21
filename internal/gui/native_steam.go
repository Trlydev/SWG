//go:build cgo && !nofyne

package gui

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// The Steam sign-in: a screen over the whole window until the person signs
// in (or chooses to go on without Steam), and the profile card in Settings.
// Signing in is Steam's QR flow: the Steam app on their phone scans the code
// shown here. No password is typed anywhere.

type steamCover struct {
	u    *ui
	root *fyne.Container
	card *fyne.Container
	sig  string
}

func newSteamCover(u *ui) *steamCover {
	c := &steamCover{u: u}
	dim := canvas.NewRectangle(color.NRGBA{0, 0, 0, 170})
	c.card = container.New(&vlist{gap: 12})
	box := container.New(&widthLayout{480}, panelWith(container.New(layout.NewCustomPaddedLayout(6, 6, 8, 8), c.card), pal().surface, 18, 22))
	c.root = container.NewStack(dim, container.NewCenter(box))
	c.root.Hide()
	return c
}

// render shows the screen while sign-in is wanted: Steam downloads are
// available here, nobody is signed in, and the person hasn't chosen to go
// on without Steam (or asked to sign in since).
func (c *steamCover) render(v steamView) {
	u := c.u
	show := v.Available && v.Account == "" && (!v.Skipped || u.steamWanted || v.Signing)
	if v.Account != "" {
		u.steamWanted = false
	}
	if !show {
		c.root.Hide()
		return
	}
	sig := fmt.Sprint(v.Signing, v.Error, strings.Join(v.QR, ""))
	changed := sig != c.sig || !c.root.Visible()
	if sig != c.sig {
		c.sig = sig
		c.card.Objects = c.content(v)
	}
	c.root.Show()
	if changed {
		// The card's frame takes the new content's height (refreshing the
		// card alone left its background at the old size).
		c.root.Refresh()
		c.root.Refresh() // wrapped text knows its height after one pass
	}
}

func (c *steamCover) content(v steamView) []fyne.CanvasObject {
	u := c.u
	title := h2("Sign in to Steam")
	switch {
	case v.Signing && len(v.QR) > 0:
		cancel := widget.NewButton("Cancel", func() { u.steamOp("cancel") })
		cancel.Importance = widget.LowImportance
		return []fyne.CanvasObject{
			title,
			text("Open the Steam app on your phone, tap the shield (Steam Guard) and scan this code."),
			container.NewCenter(qrImage(v.QR, 260)),
			muted("You only do this once. ppgmods never sees your password."),
			container.NewHBox(layout.NewSpacer(), cancel),
		}
	case v.Signing:
		cancel := widget.NewButton("Cancel", func() { u.steamOp("cancel") })
		cancel.Importance = widget.LowImportance
		return []fyne.CanvasObject{
			title,
			text("Getting a sign-in code from Steam…"),
			muted("The first time, ppgmods also fetches the small Steam downloader it uses."),
			container.NewHBox(layout.NewSpacer(), cancel),
		}
	}
	signIn := widget.NewButton("Sign in with Steam", func() { u.steamSignIn() })
	signIn.Importance = widget.HighImportance
	skip := widget.NewButton("Continue without Steam", func() {
		u.steamWanted = false
		u.steamOp("skip")
	})
	skip.Importance = widget.LowImportance
	objs := []fyne.CanvasObject{
		title,
		text("ppgmods then downloads Steam Workshop items for you, straight from Steam. You sign in once, with the Steam app on your phone: no password."),
	}
	if v.Error != "" {
		objs = append(objs, warnBox("Sign-in didn't work: "+v.Error))
	}
	return append(objs, container.NewHBox(skip, layout.NewSpacer(), signIn))
}

// qrImage draws a QR code (rows of '1' dark, '0' light) with its white
// margin, at size points square.
func qrImage(rows []string, size float32) fyne.CanvasObject {
	n := len(rows)
	const quiet = 4
	img := image.NewGray(image.Rect(0, 0, n+2*quiet, n+2*quiet))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	for y, r := range rows {
		for x := 0; x < len(r) && x < n; x++ {
			if r[x] == '1' {
				img.SetGray(x+quiet, y+quiet, color.Gray{0})
			}
		}
	}
	ci := canvas.NewImageFromImage(img)
	ci.ScaleMode = canvas.ImageScalePixels // crisp modules
	ci.FillMode = canvas.ImageFillContain
	ci.SetMinSize(fyne.NewSize(size, size))
	return ci
}

// steamSignIn starts the QR sign-in and shows the screen.
func (u *ui) steamSignIn() {
	u.steamWanted = true
	u.steamOp("signin")
}

func (u *ui) steamOp(op string) {
	go func() {
		if err := u.s.call("POST", "/api/steam", map[string]any{"op": op}, nil); err != nil {
			u.toast(err.Error())
		}
		u.refreshState()
	}()
}

// renderSteam is the Steam profile card in Settings.
func (v *settingsView) renderSteam(s steamView) {
	u := v.u
	sig := fmt.Sprint(s.Available, s.Account, s.Name, s.Avatar, s.Signing)
	if sig == v.steamSig {
		return
	}
	v.steamSig = sig
	var objs []fyne.CanvasObject
	switch {
	case !s.Available:
		objs = []fyne.CanvasObject{muted("Downloading from Steam isn't available on this computer; Steam items come from the mirrors.")}
	case s.Account != "":
		avatar := fixed(u.thumbs.box("steam-avatar:"+s.Account, s.Name, s.Avatar, 56, 56, nil, true), 56, 56)
		out := widget.NewButton("Sign out", func() {
			u.confirm("Sign out of Steam?", text("ppgmods forgets the Steam session; Steam items then come from the mirrors (or Subscribe in Steam) until you sign in again."), "Sign out", false, func() { u.steamOp("signout") })
		})
		out.Importance = widget.LowImportance
		info := container.New(&vlist{gap: -6},
			widget.NewLabelWithStyle(orStr(s.Name, s.Account), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			muted("Signed in to Steam · Steam Workshop items download by themselves"))
		objs = []fyne.CanvasObject{container.NewBorder(nil, nil, avatar, container.NewCenter(out), container.New(layout.NewCustomPaddedLayout(0, 0, 10, 0), container.New(&vcenterLayout{}, info)))}
	default:
		in := widget.NewButton("Sign in with Steam", func() { u.steamSignIn() })
		in.Importance = widget.HighImportance
		objs = []fyne.CanvasObject{
			container.NewBorder(nil, nil, nil, container.NewCenter(in),
				container.New(&vlist{gap: -6},
					widget.NewLabelWithStyle("Not signed in", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
					muted("Sign in once with the Steam app on your phone, and ppgmods downloads Steam Workshop items itself."))),
		}
	}
	v.steam.Objects = objs
	v.steam.Refresh()
}
