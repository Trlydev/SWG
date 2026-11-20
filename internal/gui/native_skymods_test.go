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
)

// The check runs out of sight: Settings says how it's going, and never
// asks the person to do anything.
func TestNativeSkymodsStatus(t *testing.T) {
	a := test.NewTempApp(t)
	a.Settings().SetTheme(newTheme())
	u := &ui{}
	u.set = &settingsView{u: u}
	settingsBox := u.set.skySettings()
	u.win = test.NewTempWindow(t, container.NewVBox(settingsBox))
	u.win.Resize(fyne.NewSize(900, 600))

	var sm skymodsState
	sm.Challenge = true
	sm.Check.At = time.Now()
	sm.Check.Error = "smods.ru wants a person"
	sm.Check.NeedsPerson = true
	u.renderSkymods(sm)
	if !strings.Contains(u.set.skyInfo, "tries again by itself") {
		t.Errorf("Settings: %q", u.set.skyInfo)
	}
	if strings.Contains(u.set.skyInfo, "Pass the check") || strings.Contains(u.set.skyInfo, "window") {
		t.Errorf("Settings still asks the person: %q", u.set.skyInfo)
	}
	sm.Check.Error, sm.Check.NeedsPerson, sm.Check.OK = "", false, true
	u.renderSkymods(sm)
	if !strings.Contains(u.set.skyInfo, "Skymods works") {
		t.Errorf("after a pass: %q", u.set.skyInfo)
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
