package gui

import (
	"encoding/json"
	"net/http/httptest"
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
