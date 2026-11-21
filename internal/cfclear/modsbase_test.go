package cfclear

import (
	"errors"
	"strings"
	"testing"
)

// The page steps run in the background browser first; when its check wants
// a person, in a window (and for a while after, straight in the window).
func TestModsbaseLink(t *testing.T) {
	const page = "https://modsbase.com/abc123def456/x.zip.html"
	var answers []string
	f := useFake(t, func(opt Options) (*Result, error) {
		a := answers[0]
		answers = answers[1:]
		if !strings.Contains(opt.Script, "cf-turnstile-response") || strings.Contains(opt.Script, "LIMIT") {
			t.Errorf("script: %.60q", opt.Script)
		}
		return &Result{HTML: a}, nil
	})
	lastPerson.Store(0)

	answers = []string{"LINK https://s1.modsbase.com/d/abc/x.zip"}
	if link, err := ModsbaseLink(page, nil); err != nil || link != "https://s1.modsbase.com/d/abc/x.zip" {
		t.Fatalf("passed by itself: %q %v", link, err)
	}
	if len(f.calls) != 1 || f.calls[0].Headed {
		t.Fatalf("want one background run: %+v", f.calls)
	}

	answers = []string{"PERSON", "LINK https://s1.modsbase.com/d/abc/y.zip"}
	if link, err := ModsbaseLink(page, nil); err != nil || !strings.HasSuffix(link, "y.zip") {
		t.Fatalf("ticked in the window: %q %v", link, err)
	}
	if len(f.calls) != 3 || !f.calls[2].Headed {
		t.Fatalf("want the window after the background try: %+v", f.calls)
	}

	answers = []string{"PERSON"} // straight to the window now
	if _, err := ModsbaseLink(page, nil); !errors.Is(err, ErrModsbasePerson) {
		t.Fatalf("not ticked: %v", err)
	}
	if len(f.calls) != 4 || !f.calls[3].Headed {
		t.Fatalf("want only the window: %+v", f.calls)
	}

	answers = []string{"ERR no download link in modsbase's reply (HTTP 200)"}
	if _, err := ModsbaseLink(page, nil); err == nil || !strings.Contains(err.Error(), "no download link") {
		t.Errorf("error from the page: %v", err)
	}
	if _, err := ModsbaseLink("https://evil.example/x", nil); err == nil {
		t.Error("ran on another site")
	}
}
