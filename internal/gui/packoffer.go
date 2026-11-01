package gui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Trlydev/SWG/internal/app"
	"github.com/Trlydev/SWG/internal/manager"
	"github.com/Trlydev/SWG/internal/modpack"
)

// A modpack (a PPG1.… code, pasted or opened as a .ppgm file) is shown in
// the window first: what it would install and what is already there.
// Nothing is installed until the person confirms; a file or a link can
// arrive from anywhere.

type packItem struct {
	Ref         string `json:"ref"`
	Name        string `json:"name"`
	Contraption bool   `json:"contraption,omitempty"`
	Off         bool   `json:"off,omitempty"`
	Installed   bool   `json:"installed,omitempty"`
}

type packOffer struct {
	Name  string     `json:"name"`
	Items []packItem `json:"items"`
	Text  string     `json:"text"` // the code, for list-import
	At    time.Time  `json:"at"`
}

// maxPackFile bounds a .ppgm file read from disk.
const maxPackFile = 64 << 10

// packEnv carries a modpack to a window started for it.
const packEnv = "PPGMODS_PACK"

// offerPack reads a modpack and shows it in the window.
func (s *server) offerPack(text string) error {
	if !modpack.Find(text) {
		return errors.New("this isn't a ppgmods modpack (no PPG1. code in it)")
	}
	l, err := app.ParseList([]byte(text))
	if err != nil {
		return err
	}
	o := &packOffer{Name: l.Profile, Text: app.ListCode(l), At: time.Now()}
	st, _ := manager.LoadState()
	for _, it := range l.Mods {
		pi := packItem{Ref: it.Ref, Name: it.Name, Contraption: it.Kind == manager.KindContraption, Off: it.Off}
		pi.Installed = st != nil && st.Find(it.Ref) != nil
		o.Items = append(o.Items, pi)
	}
	s.offerMu.Lock()
	s.packOffer = o
	s.offerMu.Unlock()
	s.logf("a modpack with %d item(s) arrived; confirm it in the window", len(o.Items))
	return nil
}

func (s *server) handlePackOffer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(io.LimitReader(r.Body, maxPackFile+1024)).Decode(&req) != nil {
		http.Error(w, "POST {text}", http.StatusBadRequest)
		return
	}
	if err := s.offerPack(req.Text); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ReadPackFile reads a .ppgm file (bounded).
func ReadPackFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxPackFile+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxPackFile {
		return "", fmt.Errorf("%s is too large to be a modpack", path)
	}
	if !modpack.Find(string(b)) {
		return "", fmt.Errorf("%s isn't a ppgmods modpack", path)
	}
	return string(b), nil
}

// OpenPack handles opening a .ppgm file: hand it to the open window, or
// start one that shows it.
func OpenPack(opt app.Options, g Options, path string) error {
	text, err := ReadPackFile(path)
	if err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		if forwardPack(text) {
			showRunning()
			return nil
		}
		if runningInstance() == "" {
			break
		}
		time.Sleep(time.Second)
	}
	os.Setenv(packEnv, text)
	return Run(opt, g)
}

func forwardPack(text string) bool {
	var in instance
	b, err := os.ReadFile(instancePath())
	if err != nil || json.Unmarshal(b, &in) != nil || in.Host == "" {
		return false
	}
	body, _ := json.Marshal(map[string]string{"text": text})
	req, _ := http.NewRequest("POST", "http://"+in.Host+"/api/pack-offer", bytes.NewReader(body))
	req.Header.Set("X-Token", in.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// IsPackPath reports whether an argument is a .ppgm file to open.
func IsPackPath(arg string) bool {
	return strings.EqualFold(filepathExt(arg), modpack.Ext)
}

func filepathExt(p string) string {
	if i := strings.LastIndexAny(p, `.\/`); i >= 0 && p[i] == '.' {
		return p[i:]
	}
	return ""
}
