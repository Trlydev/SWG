//go:build cgo && !nofyne

package gui

import (
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/Trlydev/SWG/internal/app"
)

// The cart lists mods and contraptions apart, each by name.
func TestCartGroups(t *testing.T) {
	sel := map[string]*app.SearchResult{
		"gb:2":  {Ref: "gb:2", Name: "zeta", Kind: "mod"},
		"gb:1":  {Ref: "gb:1", Name: "Alpha", Kind: "mod"},
		"sky:3": {Ref: "sky:3", Name: "Tank", Kind: "contraption"},
	}
	mods, cons := cartGroups(sel)
	if len(mods) != 2 || mods[0].Name != "Alpha" || mods[1].Name != "zeta" || len(cons) != 1 || cons[0].Ref != "sky:3" {
		t.Fatalf("mods %v, contraptions %v", mods, cons)
	}
}

// Taking an item out of the cart updates the list, the count and the card.
func TestCartRemove(t *testing.T) {
	a := test.NewTempApp(t)
	a.Settings().SetTheme(newTheme())
	w := test.NewTempWindow(t, widget.NewLabel(""))
	w.Resize(fyne.NewSize(900, 700))
	u := &ui{win: w, app: a}
	u.thumbs = newThumbLoader(u)
	b := &browseView{u: u, cards: map[string]*browseCard{}, selected: map[string]*app.SearchResult{}}
	b.selCount, b.selBar = widget.NewLabel(""), container.NewStack()
	for _, m := range []*app.SearchResult{
		{Ref: "gb:1", Name: "Alpha Weapons", Kind: "mod", Source: "GameBanana"},
		{Ref: "gb:2", Name: "Zeta Vehicles", Kind: "mod", Source: "GameBanana"},
		{Ref: "sky:3", Name: "Big Tank", Kind: "contraption", Source: "Steam Workshop"},
	} {
		b.selected[m.Ref] = m
	}
	b.showCart()
	if p := os.Getenv("CART_PNG"); p != "" {
		f, _ := os.Create(p)
		png.Encode(f, w.Canvas().Capture())
		f.Close()
	}
	if got := buttonTexts(w.Canvas().Overlays().Top()); !got["Subscribe to 3"] {
		t.Fatalf("buttons %v", got)
	}
	// Take the first one out (its delete button): the same window now
	// offers 2.
	clicked := false
	for _, k := range allObjects(w.Canvas().Overlays().Top()) {
		if bt, ok := k.(*widget.Button); ok && bt.Text == "" && !clicked {
			test.Tap(bt) // the first row's: Alpha
			clicked = true
		}
	}
	if b.selected["gb:1"] != nil {
		t.Fatal("Alpha still in the cart")
	}
	b.showCart()
	if got := buttonTexts(w.Canvas().Overlays().Top()); !got["Subscribe to 2"] {
		t.Fatalf("after removing: buttons %v", got)
	}
}

func allObjects(o fyne.CanvasObject) []fyne.CanvasObject {
	out := []fyne.CanvasObject{o}
	switch c := o.(type) {
	case *fyne.Container:
		for _, k := range c.Objects {
			out = append(out, allObjects(k)...)
		}
	case fyne.Widget:
		for _, k := range test.WidgetRenderer(c).Objects() {
			out = append(out, allObjects(k)...)
		}
	}
	return out
}

func buttonTexts(o fyne.CanvasObject) map[string]bool {
	got := map[string]bool{}
	for _, k := range allObjects(o) {
		if b, ok := k.(*widget.Button); ok {
			got[b.Text] = true
		}
	}
	return got
}
