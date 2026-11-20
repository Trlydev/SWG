//go:build cgo && !nofyne

package gui

import (
	"io"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trlydev/SWG/internal/modpack"
)

// modpacksView is the Modpacks page: share your mods as one short code,
// open someone else's, and switch between saved sets (profiles).
type modpacksView struct {
	u         *ui
	root      fyne.CanvasObject
	code      *widget.Entry
	profBar   *fyne.Container
	profSel   *widget.Select
	profNames []string
}

func newModpacks(u *ui) *modpacksView {
	p := &modpacksView{u: u}
	share := u.busyButton("Make a modpack of my mods", func() { u.run(map[string]any{"action": "list-export"}, "Saving the modpack") })
	share.Importance = widget.HighImportance
	share.Icon = theme.MailSendIcon()
	p.code = widget.NewEntry()
	p.code.SetPlaceHolder("Paste a modpack code (PPG1.…)")
	p.code.OnSubmitted = func(string) { p.openCode() }
	openBtn := widget.NewButton("Open", p.openCode)
	openFile := widget.NewButtonWithIcon("Open a .ppgm file…", theme.FolderOpenIcon(), u.openPackFile)
	openFile.Importance = widget.LowImportance
	p.profileBar()
	card := func(title, about string, body ...fyne.CanvasObject) fyne.CanvasObject {
		t := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		objs := append([]fyne.CanvasObject{t, muted(about)}, body...)
		return panelBox(container.New(&vlist{gap: 8}, objs...))
	}
	p.root = view(container.New(&vlist{gap: 16},
		h1("Modpacks"),
		narrow(760, card("Share your mods",
			"Your installed mods and contraptions as one short code, small enough for a chat message, and a .ppgm file. It names where each one comes from, nothing else.",
			container.NewHBox(share))),
		narrow(760, card("Open a modpack",
			"See what it holds before anything happens. Each mod is downloaded from its site and scanned like any install.",
			container.NewBorder(nil, nil, nil, openBtn, p.code), container.NewHBox(openFile))),
		narrow(760, card("Profiles",
			"Saved sets of the mods that are on. Switching turns the others off (moved aside, nothing deleted).",
			p.profBar)),
	))
	return p
}

// openCode shows the pasted modpack.
func (p *modpacksView) openCode() {
	v := strings.TrimSpace(p.code.Text)
	if v == "" {
		return
	}
	p.code.SetText("")
	p.u.offerPack(v)
}

// offerPack shows a modpack (code or file text) for the person to confirm.
func (u *ui) offerPack(text string) {
	go func() {
		if err := u.s.call("POST", "/api/pack-offer", map[string]any{"text": text}, nil); err != nil {
			u.toast(err.Error())
			return
		}
		u.refreshState()
	}()
}

// openPackFile picks a .ppgm file (or an older JSON mod list) and opens it.
func (u *ui) openPackFile() {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			return
		}
		defer r.Close()
		b, err := io.ReadAll(io.LimitReader(r, 1<<20+1))
		if err != nil {
			u.toast(err.Error())
			return
		}
		if modpack.Find(string(b)) {
			u.offerPack(string(b))
			return
		}
		u.run(map[string]any{"action": "list-import", "list": string(b)}, "Installing the mods of the list")
	}, u.win)
	d.SetFilter(storage.NewExtensionFileFilter([]string{modpack.Ext, ".json"}))
	d.Show()
}

// update fills the page from the state.
func (p *modpacksView) update(st *stateView) { p.renderProfiles(st) }

// profileBar is the row to switch, save and delete profiles: saved sets of
// the mods that are on.
func (p *modpacksView) profileBar() {
	u := p.u
	p.profSel = widget.NewSelect(nil, nil)
	p.profSel.PlaceHolder = "No profile"
	use := u.busyButton("Use", func() {
		if n := p.profSel.Selected; n != "" {
			u.run(map[string]any{"action": "profile-use", "name": n}, "Switching to profile "+n)
		}
	})
	save := u.busyButton("Save as…", func() {
		name := widget.NewEntry()
		name.SetPlaceHolder("e.g. Gore pack")
		name.SetText(p.profSel.Selected)
		body := container.New(&vlist{gap: 8}, text("Saves which mods are on now under this name. Switching to it later turns the other mods off (moved out of the Mods folder, nothing deleted) and these back on."), name)
		u.confirm("Save profile", body, "Save", false, func() {
			if n := strings.TrimSpace(name.Text); n != "" {
				u.run(map[string]any{"action": "profile-save", "name": n}, "Saving profile "+n)
			}
		})
	})
	del := widget.NewButton("Delete", func() {
		n := p.profSel.Selected
		if n == "" {
			return
		}
		u.confirm("Delete profile "+n+"?", text("Only the saved list is deleted; your mods stay as they are."), "Delete", true, func() {
			u.run(map[string]any{"action": "profile-delete", "name": n}, "Deleting profile "+n)
		})
	})
	del.Importance = widget.LowImportance
	label := widget.NewLabelWithStyle("Profile", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.profBar = container.NewHBox(label, narrow(260, p.profSel), use, save, del)
}

// renderProfiles fills the profile list from the state.
func (p *modpacksView) renderProfiles(st *stateView) {
	if strings.Join(st.Profiles, "\x00") != strings.Join(p.profNames, "\x00") {
		p.profNames = st.Profiles
		p.profSel.Options = st.Profiles
		p.profSel.Refresh()
	}
	if p.profSel.Selected == "" || !slices.Contains(st.Profiles, p.profSel.Selected) {
		p.profSel.Selected = ""
		if st.Profile != "" && slices.Contains(st.Profiles, st.Profile) {
			p.profSel.Selected = st.Profile
		}
		p.profSel.Refresh()
	}
}
