package app

import (
	"testing"

	"github.com/Trlydev/SWG/internal/copies"
	"github.com/Trlydev/SWG/internal/sources"
)

// True Workshop and GameBanana cards carry the Workshop item the copies
// index names, so the window folds them into that Workshop card.
func TestAnnotateCopies(t *testing.T) {
	undo := copies.SetForTest(&copies.Index{Items: map[string]copies.Entry{
		"tw:142":    {WS: "3549704863", Name: "Spider-Man", File: "abc"},
		"gb:600001": {WS: "3549704863", File: "77"},
		"tw:9":      {File: "def"},
	}})
	defer undo()
	r := SearchResults{
		TrueWS:     []SearchResult{{Ref: "tw:142", Name: "A renamed upload"}, {Ref: "tw:9"}, {Ref: "tw:10"}},
		GameBanana: []SearchResult{{Ref: "gb:600001"}},
	}
	annotateCopies(&r)
	if r.TrueWS[0].WorkshopID != "3549704863" || r.TrueWS[0].ModName != "Spider-Man" || r.GameBanana[0].WorkshopID != "3549704863" {
		t.Errorf("copies not linked: %+v %+v", r.TrueWS[0], r.GameBanana[0])
	}
	if r.TrueWS[1].WorkshopID != "" || r.TrueWS[2].WorkshopID != "" {
		t.Errorf("unlinked uploads got a Workshop ID: %+v", r.TrueWS[1:])
	}
}

func TestGBMirror(t *testing.T) {
	var m sources.GBMod
	m.ID, m.Name, m.URL = 600001, "Spider Mod", "https://gamebanana.com/mods/600001"
	m.Submitter.Name = "nova"
	f := sources.GBFile{ID: 77, Size: 2048, Added: 1700000000, Version: "1.2"}
	mr := gbMirror(m, f)
	if mr.ID != "gamebanana:600001" || mr.Source != "GameBanana" || mr.gb == nil || mr.Steam || mr.AfterCutoff {
		t.Errorf("gbMirror = %+v", mr)
	}
	if !mr.VersionTime.Equal(f.AddedTime()) || mr.Version == "" {
		t.Errorf("version from the file: %q %v", mr.Version, mr.VersionTime)
	}
}

func TestNamesAgree(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"Nova's Spider-Man Mod", "Spider-Man", true},
		{"Nova's Spider-Man Mod", "Nova's Spider-Man Mod v2.1", true},
		{"Bimba Great Company", "Great Bimba Company", true},
		{"Melee weapons pack [1.20]", "Melee Weapons", true},
		{"Arda's knife and handguns mod", "Bimba Great Company", false},
		{"Arda's Melee and guns pack", "Bimba Great Company", false},
		{"Spider-Man", "Iron Man", false},
		{"Mod", "Mod pack", false},
		{"", "anything", false},
	} {
		if got := namesAgree(c.a, c.b); got != c.want {
			t.Errorf("namesAgree(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// A mod.json reused as a template names someone else's Workshop item: the
// upload isn't taken for a copy of it unless a name agrees.
func TestCopyOf(t *testing.T) {
	w := &SearchResult{Ref: "sky:2538786225", Name: "Great Bimba Company"}
	bimba := &SearchResult{Ref: "gb:315387", Name: "Bimba Weapons and Cars", WorkshopID: "2538786225", ModName: "Bimba Great Company"}
	arda := &SearchResult{Ref: "gb:377581", Name: "Arda's knife and handguns mod", WorkshopID: "2538786225", ModName: "Arda's Melee and guns pack"}
	other := &SearchResult{Ref: "tw:1", Name: "Great Bimba Company", WorkshopID: "111111111"}
	if !CopyOf(bimba, w) {
		t.Error("the real copy (mod.json name agrees) was not linked")
	}
	if CopyOf(arda, w) {
		t.Error("a reused mod.json linked an unrelated mod")
	}
	if CopyOf(other, w) {
		t.Error("linked by title though its mod.json names another item")
	}
}
