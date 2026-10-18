//go:build cgo && !nofyne

package gui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// The native window must tell the person when smods.ru wants them to tick
// its box (it used to say so only in the log and the --web page).
func TestNativeSkymodsBanner(t *testing.T) {
	a := test.NewTempApp(t)
	a.Settings().SetTheme(newTheme())
	u := &ui{skyText: widget.NewLabel(""), skyBtn: widget.NewButton("", nil)}
	u.skyBanner = container.NewStack(u.skyText)
	u.set = &settingsView{u: u}
	settingsBox := u.set.skySettings()
	u.win = test.NewTempWindow(t, container.NewVBox(u.skyBanner, settingsBox))
	u.win.Resize(fyne.NewSize(900, 600))

	var sm skymodsState
	sm.Challenge = true
	sm.Check.At = time.Now()
	sm.Check.Error = "smods.ru wants a person"
	sm.Check.NeedsPerson = true
	u.renderSkymods(sm)
	if !u.skyBanner.Visible() || !strings.Contains(u.skyText.Text, "Verify you are human") {
		t.Fatalf("banner hidden or wrong (visible %v): %q", u.skyBanner.Visible(), u.skyText.Text)
	}
	if !u.sky.notified {
		t.Error("no popup for a check that needs the person")
	}
	if !strings.Contains(u.set.skyInfo, "tick") {
		t.Errorf("Settings doesn't say to tick the box: %q", u.set.skyInfo)
	}

	u.sky.dismissed = true
	u.renderSkymods(sm)
	if u.skyBanner.Visible() {
		t.Error("banner came back after Dismiss")
	}

	// The person presses the button; the check runs, then passes.
	u.sky.dismissed = false
	u.sky.startedAt = time.Now()
	sm.Check.Running = true
	u.renderSkymods(sm)
	if u.skyBanner.Visible() || !u.skyBtn.Disabled() {
		t.Errorf("while running: banner visible %v, button disabled %v", u.skyBanner.Visible(), u.skyBtn.Disabled())
	}
	time.Sleep(time.Millisecond)
	sm.Check.Running, sm.Check.Error, sm.Check.NeedsPerson, sm.Check.OK, sm.Check.At = false, "", false, true, time.Now()
	sm.Challenge = false
	u.renderSkymods(sm)
	if u.skyBanner.Visible() || !u.sky.startedAt.IsZero() || u.sky.notified {
		t.Errorf("after a pass: banner visible %v, startedAt %v, notified %v", u.skyBanner.Visible(), u.sky.startedAt, u.sky.notified)
	}
}

// skymodsState must read what /api/state sends.
func TestSkymodsStateDecodes(t *testing.T) {
	b, err := json.Marshal((&server{}).skymodsView())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"challenge", "clearance", "clearance_age", "check"} {
		if _, ok := m[k]; !ok {
			t.Errorf("skymodsView has no %q: %s", k, b)
		}
	}
	var sm skymodsState
	if err := json.Unmarshal(b, &sm); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(map[string]any{"check": map[string]any{"needs_person": true, "error": "x", "running": true}})
	json.Unmarshal(b, &sm)
	if !sm.Check.NeedsPerson || !sm.Check.Running || sm.Check.Error != "x" {
		t.Errorf("check fields not read: %+v", sm.Check)
	}
}
