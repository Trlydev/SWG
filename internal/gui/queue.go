package gui

import (
	"fmt"
	"time"

	"github.com/Trlydev/SWG/internal/app"
)

// Subscribing: an install asked for while another task runs waits in a
// queue and starts when that task ends, so several can be clicked one
// after another. Installed mods are then kept up to date in the background
// (Settings can turn that off); updates go through the same checks as an
// install, and one that fails them is held back.

// queueable: a plain install. One with an override, a chosen mirror or the
// typed risk phrase answers a question about that one mod and is never
// queued.
func queueable(req actionReq) bool {
	return req.Action == "install" && len(req.Refs) > 0 && len(req.Override) == 0 && req.Mirror == "" && req.Confirm == ""
}

// enqueue adds refs to the queue (once each) and returns its length.
func (s *server) enqueue(refs []string) int {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	have := map[string]bool{}
	for _, r := range s.queue {
		have[r] = true
	}
	for _, r := range refs {
		if !have[r] {
			have[r] = true
			s.queue = append(s.queue, r)
		}
	}
	return len(s.queue)
}

// startQueued starts the queued installs as one task. If another task got
// in first, they stay queued for when that one ends.
func (s *server) startQueued() {
	s.jobMu.Lock()
	refs := s.queue
	s.queue = nil
	s.jobMu.Unlock()
	if len(refs) == 0 {
		return
	}
	if _, err := s.start(actionReq{Action: "install", Refs: refs}); err != nil {
		s.enqueue(refs)
	}
}

const (
	autoUpdateFirst = 3 * time.Minute // after start: let the window and the folder check settle
	autoUpdateEvery = 6 * time.Hour
	autoUpdateRetry = 2 * time.Minute // a task was running
)

// autoUpdate keeps installed mods up to date while ppgmods runs.
func (s *server) autoUpdate() {
	t := time.NewTimer(autoUpdateFirst)
	defer t.Stop()
	for {
		select {
		case <-t.C:
		case <-s.quit:
			return
		}
		next := autoUpdateEvery
		if autoUpdateOn.Load() {
			if _, err := s.start(actionReq{Action: "update", Apply: true, background: true}); err != nil {
				next = autoUpdateRetry
			}
		}
		t.Reset(next)
	}
}

// noteAutoUpdate keeps a line about what the automatic update did, for the
// Installed tab.
func (s *server) noteAutoUpdate(sum app.Summary) {
	note := fmt.Sprintf("Checked %s: all up to date", time.Now().Format("15:04"))
	if sum.OK > 0 {
		note = fmt.Sprintf("Updated %d automatically at %s", sum.OK, time.Now().Format("15:04"))
	}
	if sum.Refused > 0 {
		note += fmt.Sprintf("; %d update(s) held back by the safety checks (Show log says why)", sum.Refused)
	}
	if sum.Failed > 0 {
		note += fmt.Sprintf("; %d could not be checked", sum.Failed)
	}
	if sum.Manual > 0 {
		note += fmt.Sprintf("; %d to update by hand on Nexus Mods", sum.Manual)
	}
	s.jobMu.Lock()
	s.autoNote = note
	s.jobMu.Unlock()
}
