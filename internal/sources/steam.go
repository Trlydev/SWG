package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The Steam Workshop still runs for People Playground: Valve removed every
// item with C# code after the September 2026 worm, but items without code
// (contraptions, scenes, sprite mods) are still uploaded and updated. Steam's
// public Web API describes any public item without a key; ppgmods uses it
// for an item's title (to find its mirror copies when Skymods is blocked)
// and to tell items still on Steam from removed ones.

// SteamAPI is the Web API base (a test hook).
var SteamAPI = "https://api.steampowered.com"

// SteamItem is what Steam says about a Workshop item.
type SteamItem struct {
	ID      string
	Live    bool      // still published on Steam (false: removed, or not public)
	Title   string    // when Live
	Updated time.Time // the item's last update, when Live
	AppID   int       // the game it belongs to, when Live
}

var (
	reDigits = regexp.MustCompile(`^\d{1,20}$`)

	steamMu    sync.Mutex
	steamCache = map[string]steamCached{}
)

type steamCached struct {
	item SteamItem
	at   time.Time
}

// SteamDetails asks Steam about Workshop items (answers kept for an hour).
// Items Steam doesn't know come back with Live false.
func SteamDetails(ids []string) (map[string]SteamItem, error) {
	out := map[string]SteamItem{}
	var ask []string
	steamMu.Lock()
	for _, id := range ids {
		if c, ok := steamCache[id]; ok && time.Since(c.at) < time.Hour {
			out[id] = c.item
		} else if reDigits.MatchString(id) {
			ask = append(ask, id)
		}
	}
	steamMu.Unlock()
	for start := 0; start < len(ask); start += 100 {
		chunk := ask[start:min(start+100, len(ask))]
		form := url.Values{"itemcount": {fmt.Sprint(len(chunk))}}
		for i, id := range chunk {
			form.Set(fmt.Sprintf("publishedfileids[%d]", i), id)
		}
		req, err := http.NewRequest("POST", SteamAPI+"/ISteamRemoteStorage/GetPublishedFileDetails/v1/", strings.NewReader(form.Encode()))
		if err != nil {
			return out, err
		}
		setHeaders(req)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			return out, err
		}
		var res struct {
			Response struct {
				Details []struct {
					ID      string `json:"publishedfileid"`
					Result  int    `json:"result"`
					Title   string `json:"title"`
					Updated int64  `json:"time_updated"`
					AppID   int    `json:"consumer_app_id"`
					Banned  int    `json:"banned"`
				} `json:"publishedfiledetails"`
			} `json:"response"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&res)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return out, fmt.Errorf("Steam Web API: HTTP %d", resp.StatusCode)
		}
		if err != nil {
			return out, fmt.Errorf("Steam Web API: %w", err)
		}
		now := time.Now()
		steamMu.Lock()
		for _, d := range res.Response.Details {
			it := SteamItem{ID: d.ID}
			if d.Result == 1 && d.Banned == 0 {
				it.Live, it.Title, it.AppID = true, strings.TrimSpace(d.Title), d.AppID
				if d.Updated > 0 {
					it.Updated = time.Unix(d.Updated, 0).UTC()
				}
			}
			out[d.ID] = it
			steamCache[d.ID] = steamCached{it, now}
		}
		steamMu.Unlock()
	}
	return out, nil
}

// SteamItemByID asks Steam about one Workshop item of People Playground; ok
// is false if Steam doesn't have it (or it belongs to another game).
func SteamItemByID(id string) (SteamItem, bool) {
	m, err := SteamDetails([]string{id})
	it := m[id]
	if err != nil || !it.Live || it.AppID != 1118200 {
		return SteamItem{ID: id}, false
	}
	return it, true
}
