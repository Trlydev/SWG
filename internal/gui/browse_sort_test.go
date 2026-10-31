//go:build cgo && !nofyne

package gui

import (
	"testing"

	"github.com/Trlydev/SWG/internal/app"
)

// Popular sorts from most to least by what the popularity data ranks: a
// mod that gained nothing this week (Rank -1) comes after every mod that
// did, however big its all-time count.
func TestMergeShownPopular(t *testing.T) {
	old := &app.SearchResult{Ref: "gb:1", Downloads: 900000, Rank: -1}
	a := &app.SearchResult{Ref: "gb:2", Downloads: 100, Rank: 50}
	b := &app.SearchResult{Ref: "sky:3", Downloads: 5000, Rank: 400}
	got := mergeShown("popular", []*app.SearchResult{old, a}, []*app.SearchResult{b})
	if got[0] != b || got[1] != a || got[2] != old {
		t.Errorf("order: %s %s %s", got[0].Ref, got[1].Ref, got[2].Ref)
	}
}
