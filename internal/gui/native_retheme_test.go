//go:build cgo && !nofyne

package gui

import (
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

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
