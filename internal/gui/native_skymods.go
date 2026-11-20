//go:build cgo && !nofyne

package gui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// skymodsState is /api/state's "skymods" (skymodsView).
type skymodsState struct {
	Challenge    bool   `json:"challenge"`
	Clearance    bool   `json:"clearance"`
	ClearanceAge string `json:"clearance_age"`
	Check        struct {
		At          time.Time `json:"at"`
		OK          bool      `json:"ok"`
		Error       string    `json:"error"`
		NeedsPerson bool      `json:"needs_person"`
		Running     bool      `json:"running"`
	} `json:"check"`
}

// The Skymods check runs only in the background, out of sight: nothing in
// the window asks the person to do anything. Settings just says how it's
// going.
func (u *ui) renderSkymods(sm skymodsState) { u.set.renderSkymods(sm) }

func (u *ui) forgetSkyCheck() {
	u.confirm("Forget the Skymods check?", text("This deletes the clearance smods.ru gave ppgmods and ppgmods' own browser profile for it. ppgmods passes the check again by itself in the background."),
		"Forget", true, func() {
			go func() {
				if err := u.s.call("DELETE", "/api/skymods", nil, nil); err != nil {
					u.toast(err.Error())
					return
				}
				u.toast("Forgot the Skymods check")
				u.refreshState()
			}()
		})
}

// skySettings is the Skymods section of Settings.
func (v *settingsView) skySettings() fyne.CanvasObject {
	v.skyForget = widget.NewButton("Forget the check", v.u.forgetSkyCheck)
	v.skyForget.Importance = widget.LowImportance
	v.skyBox = container.New(&vlist{gap: 8})
	return panelBox(v.skyBox)
}

// skyStatus is what Settings says about the check, in a line or two.
func skyStatus(sm skymodsState) string {
	switch {
	case sm.Check.Running:
		return "Passing smods.ru's check in the background right now."
	case sm.Check.Error != "" && sm.Check.NeedsPerson:
		return "smods.ru wants a person to tick its box right now, so Skymods results are missing for the moment. ppgmods tries again by itself in the background; the other sites keep working."
	case sm.Check.Error != "":
		return "The last background check didn't get through (" + sm.Check.Error + "). ppgmods tries again by itself; the other sites keep working."
	case sm.Check.OK:
		return "Skymods works: the background check passed at " + sm.Check.At.Local().Format("15:04") + "."
	case sm.Challenge:
		return "smods.ru is checking browsers right now; ppgmods reads it through an invisible background browser with its own profile (never yours)."
	}
	return "Skymods works. If smods.ru starts checking browsers, ppgmods passes the check by itself in the background, out of sight."
}

func (v *settingsView) renderSkymods(sm skymodsState) {
	if v.skyBox == nil {
		return
	}
	msg := skyStatus(sm)
	if sm.Clearance {
		msg += "\nA clearance from smods.ru is stored (got " + sm.ClearanceAge + ")."
	}
	if msg != v.skyInfo {
		// A fresh label: a wrapped one doesn't grow its row when its text changes.
		v.skyInfo = msg
		info := muted(msg)
		info.Wrapping = fyne.TextWrapWord
		v.skyBox.Objects = []fyne.CanvasObject{info, container.NewHBox(v.skyForget)}
		v.skyBox.Refresh()
		if v.root != nil {
			v.root.Refresh() // so the panel around it grows too
		}
	}
}
