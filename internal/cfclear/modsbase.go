package cfclear

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Trlydev/SWG/internal/sources"
)

// modsbase.com puts Cloudflare Turnstile ("Verify you are human") on its
// "Create download link" form, so ppgmods' own request for the link is
// refused. A real browser often passes Turnstile by itself within seconds.
// ModsbaseLink opens the file page in ppgmods' background browser, waits for
// the page's countdown and for Turnstile to finish on its own, then sends the
// page's own form (what its button does) and reads the link from the reply.
// If Turnstile wants a person to click, nothing is clicked: the caller falls
// back to the person's browser.

// ErrModsbasePerson: modsbase's Turnstile wants a person to tick its box.
var ErrModsbasePerson = errors.New(`modsbase.com's check wants a person to tick "Verify you are human"`)

// modsbaseScript runs in the modsbase file page. It returns "LINK <url>",
// "PERSON", or "ERR <why>".
const modsbaseScript = `(async () => {
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  while (document.readyState !== "complete") await sleep(100);
  const form = document.forms.F1 || document.querySelector('form[name="F1"]');
  if (!form) return "ERR the page has no download form";
  const start = Date.now();
  const cd = document.getElementById("countdown");
  const wait = ((cd && +cd.dataset.total) || 5) * 1000 + 600;
  const token = () => {
    const t = document.querySelector('[name="cf-turnstile-response"]');
    return t ? t.value : "none";
  };
  for (;;) {
    if (token() && Date.now() - start > wait) break;
    if (Date.now() - start > wait + LIMIT) return token() ? "ERR timed out" : "PERSON";
    await sleep(250);
  }
  const r = await fetch(form.getAttribute("action") || location.href, {
    method: "POST", body: new URLSearchParams(new FormData(form)), credentials: "include",
  });
  const d = new DOMParser().parseFromString(await r.text(), "text/html");
  const a = d.querySelector("a.dl2-btn") || d.querySelector(".download-details a");
  if (a && a.getAttribute("href")) return "LINK " + new URL(a.getAttribute("href"), location.href).href;
  if (d.querySelector("[data-sitekey]")) return "PERSON";
  return "ERR no download link in modsbase's reply (HTTP " + r.status + ")";
})()`

// lastPerson is when modsbase's check last wanted a person in the
// background browser: for a while after, ppgmods goes straight to the window
// (the background try costs ~20 s and fails the same way).
var lastPerson atomic.Int64

// ModsbaseLink gets a modsbase file page's download link: in the background
// browser when its check passes by itself, else in a small window of
// ppgmods' browser where the person ticks the box; the page's own steps
// (countdown, Create download link) then run by themselves.
func ModsbaseLink(page string, logf func(string, ...any)) (string, error) {
	if !strings.HasPrefix(page, "https://modsbase.com/") {
		return "", fmt.Errorf("not a modsbase file page: %s", page)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if time.Since(time.Unix(0, lastPerson.Load())) > 30*time.Minute {
		logf("  modsbase.com shows its browser check: trying ppgmods' background browser")
		link, err := modsbaseVia(page, Options{Logf: logf, Timeout: time.Minute}, 15*time.Second)
		if !errors.Is(err, ErrModsbasePerson) {
			return link, err
		}
		lastPerson.Store(time.Now().UnixNano())
	}
	logf(`  modsbase.com wants you to tick "Verify you are human": tick it in the window that opened; the download goes on by itself`)
	return modsbaseVia(page, Options{Logf: logf, Timeout: 5 * time.Minute, Headed: true}, 4*time.Minute)
}

// modsbaseVia runs the page's steps in a browser, giving its check limit.
func modsbaseVia(page string, opt Options, limit time.Duration) (string, error) {
	opt.Script = strings.Replace(modsbaseScript, "LIMIT", fmt.Sprint(limit.Milliseconds()), 1)
	res, err := open(page, opt)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(res.HTML)
	switch {
	case strings.HasPrefix(out, "LINK https://"), strings.HasPrefix(out, "LINK http://"):
		return strings.TrimPrefix(out, "LINK "), nil
	case out == "PERSON":
		return "", ErrModsbasePerson
	case strings.HasPrefix(out, "ERR "):
		return "", errors.New(strings.TrimPrefix(out, "ERR "))
	}
	return "", fmt.Errorf("unexpected answer from the page script: %.80q", out)
}

// WireModsbase lets the modsbase client use the background browser when
// modsbase shows its check.
func WireModsbase(logf func(string, ...any)) {
	sources.ModsbaseBrowserLink = func(page string) (string, error) { return ModsbaseLink(page, logf) }
}
