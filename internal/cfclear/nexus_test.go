package cfclear

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The poll survives navigation (failing evaluations), goes back when told,
// and returns the first real answer.
func TestPollPage(t *testing.T) {
	answers := []any{"", errors.New("navigating"), "BACK", "", "NXM nxm://x"}
	backs := 0
	res, err := pollPage(context.Background(), Options{Timeout: time.Minute}, func(context.Context) (string, error) {
		a := answers[0]
		answers = answers[1:]
		if e, ok := a.(error); ok {
			return "", e
		}
		return a.(string), nil
	}, func(context.Context) error { backs++; return nil }, func(error) bool { return false })
	if err != nil || res.HTML != "NXM nxm://x" || backs != 1 {
		t.Fatalf("got %+v, %v, backs=%d", res, err, backs)
	}
	// A closed window ends it at once.
	_, err = pollPage(context.Background(), Options{Timeout: time.Minute}, func(context.Context) (string, error) {
		return "", errors.New("closed the connection")
	}, nil, func(error) bool { return true })
	if err == nil {
		t.Fatal("a closed window wasn't noticed")
	}
	// Cancel ends the wait.
	cancel := make(chan struct{})
	close(cancel)
	_, err = pollPage(context.Background(), Options{Timeout: time.Minute, Cancel: cancel}, func(context.Context) (string, error) { return "", nil }, nil, func(error) bool { return false })
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestNexusLinkOnlyNexusPages(t *testing.T) {
	if _, err := NexusLink("https://evil.example/peopleplayground/mods/1", nil, nil); err == nil {
		t.Fatal("opened a page that isn't Nexus Mods")
	}
}

// In a real browser: the page clicking its own Slow download button (a
// script's click, not the person's) must not hand over the link. Needs a
// browser on this PC.
func TestNexusPollIgnoresScriptClicks(t *testing.T) {
	exe, err := FindBrowser()
	if err != nil {
		t.Skip("no browser on this machine:", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<title>Files</title>
<button id="slowDownloadButton" data-download-url="nxm://peopleplayground/mods/5/files/7?key=k&expires=1">Slow download</button>
<script>setTimeout(() => document.getElementById("slowDownloadButton").click(), 300);
setTimeout(() => { const a = document.createElement("a"); a.href = "nxm://peopleplayground/mods/5/files/7?key=k&expires=1"; a.textContent = "click here"; document.body.append(a); }, 600);</script>`))
	}))
	defer srv.Close()
	cancel := make(chan struct{})
	time.AfterFunc(5*time.Second, func() { close(cancel) })
	res, err := Run(srv.URL+"/", Options{Browser: exe, Timeout: 40 * time.Second, Poll: nexusPoll, Cancel: cancel})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("want the wait to go on until cancelled, got %+v, %v", res, err)
	}
}
