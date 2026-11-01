package gui

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Trlydev/SWG/internal/app"
)

func postAction(s *server, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/action", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleAction(w, r)
	return w
}

// Installs asked for while a task runs wait in the queue (once each) and
// start together when it ends; an install that answers a question about
// one mod (override, mirror, risk phrase) is not queued.
func TestInstallQueue(t *testing.T) {
	t.Setenv("PPGMODS_HOME", t.TempDir())
	s := &server{opt: app.DefaultOptions(), quit: make(chan struct{})}
	running := &job{ID: 99, Name: "update", Running: true}
	s.job = running

	for body, want := range map[string]int{
		`{"action":"install","refs":["gb:1","gb:2"]}`: 2,
	} {
		w := postAction(s, body)
		var j job
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &j) != nil || j.ID != 0 || j.Queued != want {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	w := postAction(s, `{"action":"install","refs":["gb:2","gb:3"]}`)
	if !strings.Contains(w.Body.String(), `"queued":3`) {
		t.Fatalf("duplicate not merged: %s", w.Body)
	}
	for _, body := range []string{
		`{"action":"install","refs":["gb:4"],"override":{"allow_high":true}}`,
		`{"action":"install","refs":["gb:4"],"mirror":"x"}`,
		`{"action":"remove","key":"gb:1"}`,
	} {
		if w := postAction(s, body); w.Code != 409 {
			t.Errorf("%s: want 409 (busy), got %d %s", body, w.Code, w.Body)
		}
	}
	if got := strings.Join(s.queue, ","); got != "gb:1,gb:2,gb:3" {
		t.Fatalf("queue %q", got)
	}

	// The running task ends: the queue starts as one install.
	s.jobMu.Lock()
	running.Running = false
	s.jobMu.Unlock()
	s.startQueued()
	s.jobMu.Lock()
	j, left := s.job, len(s.queue)
	s.jobMu.Unlock()
	if j == running || j.Name != "install" || left != 0 {
		t.Fatalf("queued installs not started: %+v, %d left", j, left)
	}
	waitIdle(t, s)
}

// While a task runs, the queued installs stay queued for when it ends.
func TestStartQueuedBusy(t *testing.T) {
	s := &server{opt: app.DefaultOptions(), quit: make(chan struct{})}
	s.job = &job{Running: true}
	s.enqueue([]string{"gb:1"})
	s.startQueued()
	if len(s.queue) != 1 {
		t.Fatal("queue lost while a task runs")
	}
}

func waitIdle(t *testing.T, s *server) {
	t.Helper()
	for i := 0; i < 200; i++ {
		s.jobMu.Lock()
		busy := s.job != nil && s.job.Running
		s.jobMu.Unlock()
		if !busy {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("task didn't end")
}

func TestAutoUpdateNote(t *testing.T) {
	s := &server{}
	s.noteAutoUpdate(app.Summary{Current: 4})
	if !strings.Contains(s.autoNote, "all up to date") {
		t.Errorf("note %q", s.autoNote)
	}
	s.noteAutoUpdate(app.Summary{OK: 2, Refused: 1})
	if !strings.Contains(s.autoNote, "Updated 2") || !strings.Contains(s.autoNote, "1 update(s) held back") {
		t.Errorf("note %q", s.autoNote)
	}
}

// The setting is saved and read back; no setting means on.
func TestAutoUpdateSetting(t *testing.T) {
	t.Setenv("PPGMODS_HOME", t.TempDir())
	s := &server{opt: app.DefaultOptions(), quit: make(chan struct{})}
	defer autoUpdateOn.Store(true)
	for body, want := range map[string]bool{`{"no_auto_update":true}`: false, `{}`: true} {
		r := httptest.NewRequest("POST", "/api/settings", strings.NewReader(body))
		s.handleSettings(httptest.NewRecorder(), r)
		if autoUpdateOn.Load() != want {
			t.Errorf("%s: auto update %v, want %v", body, autoUpdateOn.Load(), want)
		}
	}
}

// A pasted code or a .ppgm file is shown first, never installed straight
// away; anything that isn't a modpack is refused.
func TestPackOffer(t *testing.T) {
	t.Setenv("PPGMODS_HOME", t.TempDir())
	s := &server{opt: app.DefaultOptions(), quit: make(chan struct{})}
	code := app.ListCode(&app.ModList{Profile: "war", Mods: []app.ModListItem{{Ref: "gb:1", Name: "A"}, {Ref: "tw:2", Name: "B", Kind: "contraption"}}})
	r := httptest.NewRequest("POST", "/api/pack-offer", strings.NewReader(`{"text":"try `+code+`"}`))
	w := httptest.NewRecorder()
	s.handlePackOffer(w, r)
	if w.Code != 200 || s.packOffer == nil || s.packOffer.Name != "war" || len(s.packOffer.Items) != 2 || !s.packOffer.Items[1].Contraption {
		t.Fatalf("%d %s %+v", w.Code, w.Body, s.packOffer)
	}
	if s.job != nil {
		t.Fatal("an offer must not start an install")
	}
	for _, body := range []string{`{"text":"gb:1"}`, `{"text":"PPG1.!!"}`, `{"text":"{\"format\":\"ppgmods-mod-list\",\"mods\":[]}"}`} {
		w := httptest.NewRecorder()
		s.handlePackOffer(w, httptest.NewRequest("POST", "/api/pack-offer", strings.NewReader(body)))
		if w.Code != 400 {
			t.Errorf("%s: %d", body, w.Code)
		}
	}

	dir := t.TempDir()
	good, big, other := dir+"/a.ppgm", dir+"/b.ppgm", dir+"/c.ppgm"
	os.WriteFile(good, []byte(code+"\n"), 0o644)
	os.WriteFile(big, []byte(strings.Repeat("x", maxPackFile+1)), 0o644)
	os.WriteFile(other, []byte("hello"), 0o644)
	if txt, err := ReadPackFile(good); err != nil || !strings.Contains(txt, code) {
		t.Errorf("good file: %v", err)
	}
	for _, p := range []string{big, other, dir + "/missing.ppgm"} {
		if _, err := ReadPackFile(p); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
	for arg, want := range map[string]bool{"a.ppgm": true, `C:\x\A.PPGM`: true, "gui": false, "x.ppgm.exe": false, ".ppgm/x": false} {
		if IsPackPath(arg) != want {
			t.Errorf("IsPackPath(%q) = %v", arg, !want)
		}
	}
}
