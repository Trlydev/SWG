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

// skymodsWatch is what the window remembers about the check between polls.
type skymodsWatch struct {
	dismissed bool      // the banner was dismissed for this block
	notified  bool      // the "needs you" popup was shown for this block
	startedAt time.Time // when the person pressed "Pass the check now"
}

// skyStuck reports whether Skymods is blocked and waits on the person: the
// last background read failed and nothing is running now.
func skyStuck(sm skymodsState) bool {
	return !sm.Check.Running && sm.Check.Error != "" && (sm.Challenge || sm.Check.NeedsPerson)
}

func skyBannerText(sm skymodsState) string {
	if sm.Check.NeedsPerson {
		return "Skymods needs you: smods.ru wants a person to tick its \"Verify you are human\" box. Press the button, a browser window opens, tick the box there and it closes by itself. Other sites keep working meanwhile."
	}
	return "smods.ru is blocking Skymods and the background browser didn't get through (" + sm.Check.Error + "). Press the button to try again in a window. Other sites keep working."
}

// renderSkymods shows the banner (and once, a popup) when Skymods waits on
// the person, and reports how a check they started went.
func (u *ui) renderSkymods(sm skymodsState) {
	w := &u.sky
	stuck := skyStuck(sm)
	if !stuck && !sm.Check.Running {
		w.dismissed, w.notified = false, false
	}
	if stuck && !w.dismissed {
		u.skyText.SetText(skyBannerText(sm))
		u.skyBanner.Show()
	} else {
		u.skyBanner.Hide()
	}
	if sm.Check.Running {
		u.skyBtn.SetText("Check running…")
		u.skyBtn.Disable()
	} else {
		u.skyBtn.SetText("Pass the check now")
		u.skyBtn.Enable()
	}
	if stuck && sm.Check.NeedsPerson && !w.notified && !w.dismissed && w.startedAt.IsZero() {
		w.notified = true
		u.toastAction("Skymods needs you to tick a \"Verify you are human\" box.", "Open it", u.runSkyCheck)
	}
	if !w.startedAt.IsZero() && !sm.Check.Running && sm.Check.At.After(w.startedAt) {
		w.startedAt = time.Time{}
		if sm.Check.OK {
			u.toast("Skymods check passed. Search again to see Skymods results.")
		} else {
			u.toast("The Skymods check did not pass: " + sm.Check.Error)
		}
	}
	u.set.renderSkymods(sm)
}

// runSkyCheck starts the check: in the background first, then in a window
// where the person ticks the box.
func (u *ui) runSkyCheck() {
	u.sky.dismissed = false
	u.sky.startedAt = time.Now() // before the POST: a quick pass must still count
	u.skyBtn.SetText("Check running…")
	u.skyBtn.Disable()
	go func() {
		if err := u.s.call("POST", "/api/skymods", map[string]any{}, nil); err != nil {
			fyne.Do(func() { u.sky.startedAt = time.Time{} })
			u.toast(err.Error())
			u.refreshState()
			return
		}
		u.toast("Checking smods.ru. If a browser window opens, tick \"Verify you are human\" in it; it closes by itself.")
		time.Sleep(1500 * time.Millisecond)
		u.refreshState()
	}()
}

func (u *ui) forgetSkyCheck() {
	u.confirm("Forget the Skymods check?", text("This deletes the clearance smods.ru gave ppgmods and ppgmods' own browser profile for it. The next Skymods search may ask you to tick the box again."),
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
	v.skyCheck = widget.NewButton("Pass the check now", v.u.runSkyCheck)
	v.skyCheck.Importance = widget.HighImportance
	v.skyForget = widget.NewButton("Forget the check", v.u.forgetSkyCheck)
	v.skyForget.Importance = widget.LowImportance
	v.skyBox = container.New(&vlist{gap: 8})
	return panelBox(v.skyBox)
}

func (v *settingsView) renderSkymods(sm skymodsState) {
	if v.skyBox == nil {
		return
	}
	msg := "smods.ru isn't asking ppgmods for a browser check right now."
	if sm.Challenge {
		msg = "smods.ru is showing its browser check to ppgmods right now. Its pages are read through a background browser with ppgmods' own profile (never yours)."
	}
	switch {
	case sm.Check.Running:
		msg += "\nA browser is passing the check right now. If a window opens, tick \"Verify you are human\" in it."
	case sm.Check.NeedsPerson:
		msg += "\nIt wants a person to tick its \"Verify you are human\" box: press Pass the check now."
	case sm.Check.Error != "":
		msg += "\nLast check failed: " + sm.Check.Error
	case sm.Check.OK:
		msg += "\nLast check passed at " + sm.Check.At.Local().Format("15:04") + "."
	}
	if sm.Clearance {
		msg += "\nA clearance from smods.ru is stored (got " + sm.ClearanceAge + ")."
	}
	if msg != v.skyInfo {
		// A fresh label: a wrapped one doesn't grow its row when its text changes.
		v.skyInfo = msg
		info := muted(msg)
		info.Wrapping = fyne.TextWrapWord
		v.skyBox.Objects = []fyne.CanvasObject{info, container.NewHBox(v.skyCheck, v.skyForget)}
		v.skyBox.Refresh()
		if v.root != nil {
			v.root.Refresh() // so the panel around it grows too
		}
	}
	if sm.Check.Running {
		v.skyCheck.SetText("Check running…")
		v.skyCheck.Disable()
	} else {
		v.skyCheck.SetText("Pass the check now")
		v.skyCheck.Enable()
	}
}
