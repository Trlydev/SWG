package copies

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

const sample = `{"generated":"2026-11-20T01:30:00Z","items":{
 "tw:142":{"ws":"3549704863","file":"abc"},
 "gb:600001":{"ws":"3549704863","file":"77","mod":1700000000},
 "tw:9":{"file":"def"},
 "tw:bad":{"ws":"3549704863","file":"x"},
 "sky:1":{"ws":"3549704863","file":"x"},
 "gb:5":{"ws":"12ab","file":"x"}}}`

func TestParse(t *testing.T) {
	ix, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Items) != 3 {
		t.Fatalf("kept %d entries, want 3 (malformed ones dropped): %v", len(ix.Items), ix.Items)
	}
	if got := ix.WorkshopOf("tw:142"); got != "3549704863" {
		t.Errorf("WorkshopOf(tw:142) = %q", got)
	}
	if got := ix.WorkshopOf("tw:9"); got != "" {
		t.Errorf("a contraption names no Workshop item, got %q", got)
	}
	c := ix.CopiesOf("3549704863")
	slices.Sort(c)
	if !slices.Equal(c, []string{"gb:600001", "tw:142"}) {
		t.Errorf("CopiesOf = %v", c)
	}
	var nilIx *Index
	if nilIx.WorkshopOf("tw:1") != "" || nilIx.CopiesOf("1") != nil {
		t.Error("a nil index should know nothing")
	}
	if _, err := Parse([]byte("not json")); err == nil {
		t.Error("bad JSON accepted")
	}
}

func TestFetchCachesAndFallsBack(t *testing.T) {
	hits := 0
	up := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if !up {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		w.Write([]byte(sample))
	}))
	defer srv.Close()
	dir := t.TempDir()
	oldURL, oldDir := URL, CacheDir
	URL, CacheDir = srv.URL, dir
	defer func() { URL, CacheDir = oldURL, oldDir; cached = nil }()

	cached = nil
	ix, err := Fetch()
	if err != nil || ix.WorkshopOf("tw:142") == "" {
		t.Fatalf("Fetch: %v %v", ix, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "copies.json")); err != nil {
		t.Errorf("index not kept on disk: %v", err)
	}
	Fetch()
	if hits != 1 {
		t.Errorf("fetched %d times within six hours, want 1", hits)
	}
	// Offline, a fresh start: the copy on disk is used.
	up, cached, fetched = false, nil, time.Time{}
	ix, err = Fetch()
	if err != nil || ix.WorkshopOf("gb:600001") != "3549704863" {
		t.Fatalf("offline Fetch: %v %v", ix, err)
	}
}
