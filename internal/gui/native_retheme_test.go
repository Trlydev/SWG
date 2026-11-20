//go:build cgo && !nofyne

package gui

import (
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"github.com/Trlydev/SWG/internal/app"
)

// Changing Appearance switches the window at once (it used to wait for the
// next start, which looked like Light and Match system did nothing).
func TestRethemeSwitchesAtOnce(t *testing.T) {
	t.Setenv("PPGMODS_HOME", t.TempDir())
	defer func() { windowTheme = "dark" }()
	a := test.NewTempApp(t)
	windowTheme = "dark"
	a.Settings().SetTheme(newTheme())
	s := &server{opt: app.DefaultOptions(), quit: make(chan struct{}), host: "127.0.0.1:1", token: "t"}
	s.handler = s.routes()
	u := &ui{s: s, app: a}
	u.thumbs = newThumbLoader(u)
	u.win = test.NewTempWindow(t, nil)
	u.win.Resize(fyne.NewSize(1320, 840))
	u.build()
	if isDark() != true {
		t.Fatal("not dark at start")
	}
	u.rebuildIn("light")
	if isDark() || pal() != &lightPal {
		t.Fatal("still dark after switching to light")
	}
	if len(u.navs) != pageSettings+1 || !u.views[pageSettings].Visible() {
		t.Fatalf("window not rebuilt on Settings: %d navs", len(u.navs))
	}
	if p := os.Getenv("THEME_PNG"); p != "" {
		f, _ := os.Create(p)
		png.Encode(f, u.win.Canvas().Capture())
		f.Close()
	}
}

// With "Match system", the window is rebuilt in the system's new colours
// when it switches between light and dark, on the page that was open; with
// a fixed Appearance it isn't.
func TestFollowsSystemTheme(t *testing.T) {
	t.Setenv("PPGMODS_HOME", t.TempDir())
	defer func() { windowTheme = "dark" }()
	a := test.NewTempApp(t)
	windowTheme = "system"
	a.Settings().SetTheme(newTheme())
	s := &server{opt: app.DefaultOptions(), quit: make(chan struct{}), host: "127.0.0.1:1", token: "t"}
	s.handler = s.routes()
	u := &ui{s: s, app: a}
	u.thumbs = newThumbLoader(u)
	u.win = test.NewTempWindow(t, nil)
	u.win.Resize(fyne.NewSize(1320, 840))
	u.build()
	u.sysVariant = theme.VariantDark
	u.show(pageSafety)
	before := u.views[pageSafety]
	if u.systemChanged(theme.VariantDark) || u.views[pageSafety] != before {
		t.Error("rebuilt although the system didn't change")
	}
	if !u.systemChanged(theme.VariantLight) || u.views[pageSafety] == before {
		t.Fatal("not rebuilt when the system switched to light")
	}
	if !u.views[pageSafety].Visible() || u.page != pageSafety {
		t.Errorf("rebuilt, but not on the page that was open (page %d)", u.page)
	}
	windowTheme = "dark"
	before = u.views[pageSafety]
	if u.systemChanged(theme.VariantDark) || u.views[pageSafety] != before {
		t.Error("rebuilt with a fixed Appearance")
	}
}
