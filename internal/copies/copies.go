// Package copies is the index of which uploads on other sites are copies of
// which Steam Workshop item: True Workshop and GameBanana uploads, by the
// CreatorUGCIdentity in their mod.json. A GitHub Action (cmd/copyindex)
// reads each upload's mod.json once, a few KB of it (sources.RemoteModJSON),
// and publishes copies.json on the copies-data branch; ppgmods reads that
// file, so a mod shows as one card with all its copies without anyone
// downloading anything. A copy is still checked against its mod.json when it
// is downloaded to install (an index entry never makes a copy installable
// on its own).
package copies

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// IndexURL is where the Action publishes the index.
const IndexURL = "https://raw.githubusercontent.com/Trlydev/SWG/copies-data/copies.json"

// Entry is what is known about one upload.
type Entry struct {
	WS   string `json:"ws,omitempty"`   // the Workshop item its mod.json names ("": none, e.g. a contraption)
	Name string `json:"name,omitempty"` // the Name in its mod.json: mod.json files get copied as templates, Workshop ID and all, so ppgmods trusts WS only when the names agree
	File string `json:"file"`           // which file was read (True Workshop SHA-256, GameBanana file id): read again when it changes
	Mod  int64  `json:"mod,omitempty"`  // GameBanana: the upload's modified time when it was read
}

// Entry returns what is known about the upload ref.
func (ix *Index) Entry(ref string) (Entry, bool) {
	if ix == nil {
		return Entry{}, false
	}
	e, ok := ix.Items[ref]
	return e, ok
}

// Index maps an upload's ref (tw:<id>, gb:<id>) to what it is.
type Index struct {
	Generated time.Time        `json:"generated"`
	Items     map[string]Entry `json:"items"`

	byWS map[string][]string
}

// WorkshopOf is the Workshop item an upload is a copy of ("" if unknown).
func (ix *Index) WorkshopOf(ref string) string {
	if ix == nil {
		return ""
	}
	return ix.Items[ref].WS
}

// CopiesOf lists the uploads that are copies of Workshop item ws.
func (ix *Index) CopiesOf(ws string) []string {
	if ix == nil || ws == "" {
		return nil
	}
	if ix.byWS == nil {
		ix.byWS = map[string][]string{}
		for ref, e := range ix.Items {
			if e.WS != "" {
				ix.byWS[e.WS] = append(ix.byWS[e.WS], ref)
			}
		}
	}
	return ix.byWS[ws]
}

var (
	mu      sync.Mutex
	cached  *Index
	fetched time.Time
	// CacheDir keeps the last index for offline use ("" disables).
	CacheDir string
	// URL is the index to read (a test hook).
	URL    = ""
	client = &http.Client{Timeout: 30 * time.Second}
)

// Fetch returns the published index, refreshed at most every six hours (it
// changes once a day). It returns nil and an error when none is available.
func Fetch() (*Index, error) {
	mu.Lock()
	defer mu.Unlock()
	if cached != nil && time.Since(fetched) < 6*time.Hour {
		return cached, nil
	}
	ix, err := download()
	if err == nil {
		cached, fetched = ix, time.Now()
		return ix, nil
	}
	if cached != nil {
		return cached, nil
	}
	if CacheDir != "" {
		if b, rerr := os.ReadFile(filepath.Join(CacheDir, "copies.json")); rerr == nil {
			var ix Index
			if json.Unmarshal(b, &ix) == nil {
				cached, fetched = &ix, time.Now().Add(-5*time.Hour) // retry within the hour
				return cached, nil
			}
		}
	}
	return nil, err
}

// Cached is the index already fetched (nil if none yet): for code that
// mustn't wait on the network.
func Cached() *Index {
	mu.Lock()
	defer mu.Unlock()
	return cached
}

func download() (*Index, error) {
	u := URL
	if u == "" {
		u = IndexURL
	}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "ppgmods")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("no copies index published yet")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copies index: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	ix, err := Parse(b)
	if err != nil {
		return nil, err
	}
	if CacheDir != "" {
		os.WriteFile(filepath.Join(CacheDir, "copies.json"), b, 0o644)
	}
	return ix, nil
}

// Parse reads an index, keeping only well-formed entries (refs tw:/gb: with
// a number, Workshop IDs of 6 to 12 digits).
func Parse(b []byte) (*Index, error) {
	var ix Index
	if err := json.Unmarshal(b, &ix); err != nil {
		return nil, fmt.Errorf("copies index: %w", err)
	}
	for ref, e := range ix.Items {
		if !validRef(ref) || e.WS != "" && !digits(e.WS, 6, 12) {
			delete(ix.Items, ref)
		}
	}
	if ix.Items == nil {
		ix.Items = map[string]Entry{}
	}
	return &ix, nil
}

func validRef(ref string) bool {
	for _, p := range []string{"tw:", "gb:"} {
		if id, ok := strings.CutPrefix(ref, p); ok {
			return digits(id, 1, 12)
		}
	}
	return false
}

func digits(s string, lo, hi int) bool {
	return len(s) >= lo && len(s) <= hi && strings.Trim(s, "0123456789") == ""
}

// SetForTest makes Fetch return ix without the network; the returned
// function restores the previous state.
func SetForTest(ix *Index) func() {
	mu.Lock()
	defer mu.Unlock()
	oc, of := cached, fetched
	cached, fetched = ix, time.Now().Add(100*365*24*time.Hour)
	return func() {
		mu.Lock()
		cached, fetched = oc, of
		mu.Unlock()
	}
}
