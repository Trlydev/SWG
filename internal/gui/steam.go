package gui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Trlydev/SWG/internal/app"
	"github.com/Trlydev/SWG/internal/game"
	"github.com/Trlydev/SWG/internal/sources"
	"github.com/Trlydev/SWG/internal/steamdl"
)

// The Steam account ppgmods downloads Steam Workshop items with. Signing in
// is Steam's QR flow (the Steam app on a phone scans the code shown in the
// window), once; the session is kept until Steam ends it or the person signs
// out. Nothing here ever sees a password.

// steamView is /api/state's "steam".
type steamView struct {
	Available bool     `json:"available"`         // DepotDownloader has a build for this computer
	Account   string   `json:"account,omitempty"` // signed in as
	Name      string   `json:"name,omitempty"`    // Steam profile name
	Avatar    string   `json:"avatar,omitempty"`
	Signing   bool     `json:"signing,omitempty"` // a sign-in is waiting for the scan
	QR        []string `json:"qr,omitempty"`      // the code to show: rows of '1' (dark) and '0'
	Error     string   `json:"error,omitempty"`   // why the last sign-in didn't work
	Skipped   bool     `json:"skipped,omitempty"` // "Continue without Steam" was chosen
}

var steamSess struct {
	sync.Mutex
	signing bool
	qr      []string
	err     string
	cancel  context.CancelFunc
	account string // checked at most every few seconds (reads a file)
	checked time.Time
	profile map[string]sources.SteamProfile
}

func steamAvailable() bool {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64", "darwin/amd64", "darwin/arm64":
		return true
	}
	return false
}

func (s *server) steamState() steamView {
	v := steamView{Available: steamAvailable()}
	v.Skipped = steamSkipped.Load()
	c := &steamSess
	c.Lock()
	defer c.Unlock()
	if time.Since(c.checked) > 5*time.Second {
		c.account, c.checked = app.SteamTool().Account(), time.Now()
	}
	v.Account, v.Signing, v.Error = c.account, c.signing, c.err
	if c.signing {
		v.QR = c.qr
	}
	if v.Account != "" {
		if p, ok := c.profile[v.Account]; ok {
			v.Name, v.Avatar = p.Name, p.Avatar
		} else {
			if c.profile == nil {
				c.profile = map[string]sources.SteamProfile{}
			}
			c.profile[v.Account] = sources.SteamProfile{Name: v.Account}
			v.Name = v.Account
			go loadSteamProfile(v.Account)
		}
	}
	return v
}

// loadSteamProfile fetches the profile name and avatar of an account Steam
// on this PC knows (by its SteamID in loginusers.vdf).
func loadSteamProfile(account string) {
	id := game.SteamIDOf(account)
	if id == "" {
		return
	}
	p, err := sources.SteamProfileOf(id)
	if err != nil {
		return
	}
	steamSess.Lock()
	steamSess.profile[account] = p
	steamSess.Unlock()
}

// handleSteam: POST {"op": "signin" | "cancel" | "signout" | "skip" | "unskip"}.
func (s *server) handleSteam(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, s.steamState())
		return
	}
	var req struct {
		Op string `json:"op"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c := &steamSess
	switch req.Op {
	case "signin":
		if !steamAvailable() {
			http.Error(w, steamdl.ErrUnsupported.Error(), http.StatusBadRequest)
			return
		}
		c.Lock()
		if c.signing {
			c.Unlock()
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		c.signing, c.qr, c.err, c.cancel = true, nil, "", cancel
		c.Unlock()
		s.logf("signing in to Steam: scan the QR code in the window with the Steam app")
		go func() {
			defer cancel()
			acct, err := app.SteamTool().SignIn(ctx, func(rows []string) {
				c.Lock()
				c.qr = rows
				c.Unlock()
			})
			c.Lock()
			c.signing, c.qr, c.cancel, c.checked = false, nil, nil, time.Time{}
			switch {
			case err == nil:
				c.account = acct
				s.logf("signed in to Steam as %s: Steam Workshop items now download by themselves", acct)
			case errors.Is(err, context.Canceled):
			default:
				c.err = err.Error()
				s.logf("Steam sign-in: %v", err)
			}
			c.Unlock()
		}()
	case "cancel":
		c.Lock()
		if c.cancel != nil {
			c.cancel()
		}
		c.Unlock()
	case "signout":
		c.Lock()
		if c.cancel != nil {
			c.cancel()
		}
		c.account, c.checked = "", time.Time{}
		c.Unlock()
		if err := app.SteamTool().SignOut(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.logf("signed out of Steam")
	case "skip", "unskip":
		steamSkipped.Store(req.Op == "skip")
		s.optMu.RLock()
		st := settingsFrom(s.opt)
		s.optMu.RUnlock()
		if err := st.save(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "unknown op "+strings.TrimSpace(req.Op), http.StatusBadRequest)
		return
	}
	writeJSON(w, s.steamState())
}
