package cfclear

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Nexus Mods gives a free account a file only from the mod's page: the
// person opens it and clicks "Slow download" (Nexus's terms and its API both
// draw the line there; premium accounts download through the API). With
// ppgmods handling nxm:// links, the page opens as a "mod manager download"
// and the button carries the file's one-time nxm:// link, which ppgmods
// turns into the download.
//
// NexusLink opens that page in a small window of ppgmods' own browser, so
// the person needn't switch to their browser or find the file: they sign in
// to Nexus there once (the window keeps ppgmods' profile) and click Slow
// download. The window only watches: it takes the link after the person's
// own click on the button (a click the browser marks as trusted, which no
// script can fake), never before and never by clicking.

// nexusPoll runs in the window's page every half second. It answers
// "NXM <link>" after the person clicked Slow download, "BACK" when they
// ended up elsewhere on Nexus (signing in can land on the home page), else
// "" (keep waiting).
const nexusPoll = `(() => {
  const S = window.__ppgmodsNX || (window.__ppgmodsNX = { link: "", clicked: 0 });
  const nxmOf = (el) => {
    for (const a of ["href", "data-download-url", "data-url"]) {
      const v = el.getAttribute && el.getAttribute(a);
      if (v && v.startsWith("nxm://")) return v;
    }
    return "";
  };
  if (!S.on) {
    S.on = true;
    document.addEventListener("click", (e) => {
      if (!e.isTrusted) return; // only the person's own click counts
      for (let el = e.target; el && el !== document; el = el.parentElement) {
        const v = nxmOf(el);
        const slow = /slow\s*download/i.test(el.textContent || "") || /slow/i.test(el.id || "");
        if (v && slow) { S.link = v; return; }
        if (slow && (el.tagName === "BUTTON" || el.tagName === "A")) { S.clicked = Date.now(); return; }
      }
    }, true);
  }
  if (S.link) return "NXM " + S.link;
  if (S.clicked && Date.now() - S.clicked < 30000) {
    // The button made the link after the click: it shows as a link on the page.
    const a = document.querySelector('a[href^="nxm://"]');
    if (a) return "NXM " + a.getAttribute("href");
  }
  if (location.hostname === "www.nexusmods.com" && !location.pathname.startsWith("/peopleplayground/mods/")) return "BACK";
  return "";
})()`

// NexusPage is the start of every page NexusLink opens.
const NexusPage = "https://www.nexusmods.com/peopleplayground/mods/"

// NexusLink opens a Nexus Mods file page in a window and returns the nxm://
// link the page gives once the person clicks Slow download. Closing cancel
// closes the window (the link arrived another way).
func NexusLink(page string, cancel <-chan struct{}, logf func(string, ...any)) (string, error) {
	if !strings.HasPrefix(page, NexusPage) {
		return "", fmt.Errorf("not a People Playground page on Nexus Mods: %s", page)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	logf(`  Nexus Mods: click "Slow download" in the window that opened (sign in to Nexus there the first time); ppgmods takes the file from there`)
	res, err := open(page, Options{Logf: logf, Headed: true, Timeout: 15 * time.Minute, Poll: nexusPoll, Cancel: cancel})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(res.HTML)
	if link, ok := strings.CutPrefix(out, "NXM "); ok && strings.HasPrefix(link, "nxm://") {
		return link, nil
	}
	return "", errors.New("the Nexus page gave no download link")
}
