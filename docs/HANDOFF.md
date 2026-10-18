# Handoff (after v0.3.4)

State of ppgmods (People Playground mod manager, Go) for the next session.

## Repo and releases

- Repo: github.com/Trlydev/SWG (the git remote DogeKingC/SWG redirects there).
  Go module path: `github.com/Trlydev/SWG`.
- Latest release: **v0.3.4** (main at f7441d0). `build.yml` publishes
  v0.3.N on every push to main (counted from the v0.3.* tags); pushes to
  other branches don't build.
- Work branch: `claude/trusting-babbage-cu3pyj`.

## Rules from the owner

- Push straight to main only when the owner says "push". Otherwise commit
  to the work branch. No PRs.
- Push only when the full `go test ./...` passes.
- Few commits: one per batch of fixes or per real feature.
- Commit messages carry no Claude session links. The
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` line is fine.
- Explain things short and plain.

## Releases so far

| Version | What |
|---|---|
| 0.3.0 | Mod profiles, turn mods off/on, export/import mod lists, Open Workshop review queue, RE_PPG approvals check, Workshop copy first for Nexus |
| 0.3.1 | Review fixes; 01 STUDIO CDN downloads (free mods) |
| 0.3.2 | Update counts every item; DLL capability analysis (reads .NET metadata) |
| 0.3.3 | 01 STUDIO mods labelled "01 STUDIO", downloaded only from 01 STUDIO |
| 0.3.4 | Skymods (smods.ru) Cloudflare check fixed (see below) |

## Skymods check (0.3.4), `internal/cfclear`

- **The finding.** smods.ru's check is an interactive Turnstile box
  ("Verify you are human"). It never passes by itself in a driven browser
  (at least from the cloud container). Left alone, it times out after about
  2 minutes and leaves a useless cookie.
- **No cookie handoff.** smods.ru refuses a fresh cf_clearance from Go's
  HTTP client (403 even with the same User-Agent and IP). So pages are read
  **through the browser** (`BrowserGet` → `sources.SkyBrowserGet`), using
  ppgmods' own persistent profile (`<config>/browser/ppgmods-<browser>`).
- **What counts as a pass.** Only the real page loading counts:
  readyState is done, there's no `_cf_chl_opt` or challenge script, and the
  title isn't "Just a moment".
- **Background mode.** It gives up after 25 s with `ErrNeedsPerson`, then
  waits 10 minutes before trying the background browser again.
  - The "Pass the check now" button (or `ppgmods skymods-check`) runs
    `Check`: background first, then a visible window where the person
    ticks the box. That pass is kept in the profile.
  - "Forget the check" deletes the stored clearance and the profile.
- **Untested.** Real tick → then Skymods searches work. It couldn't be tried
  here, because the box needs a person. We deliberately never auto-tick it.
- **Ask the owner.** Did ticking it in the window work on their PC (Zen
  browser on Linux)?

## Other key facts

- **Nexus.**
  - Free accounts can't get download links from the API.
  - Installs open the file page with `nmm=1`, and the nxm link is handed
    back to ppgmods (`app.DeliverNXM`).
- **01 STUDIO.**
  - Free mods come from `https://cdn.01studio.dev/files/download/<WorkshopID>.zip`.
  - The s01 mirror is tried first and alone.
  - Their mods use the ZeroOne loader (Main.dll via reflection), so the
    scanner flags them CRITICAL.
- **Turned-off mods** move by rename into `<game>/ppgmods-off/<key>`.
  Before turning one back on, ppgmods checks that it wasn't withdrawn or
  blocklisted and that its files haven't changed.
- **Scanner** (`internal/scan`): every DLL is at least HIGH
  ("compiled-code"). Native or unreadable DLLs are CRITICAL
  ("executable-file"). The "What it can do" line comes from the DLL's
  metadata (`dllcaps.go`, `clr.go`).
- **Testing browsers in the cloud container.**
  - Chromium: `/opt/pw-browsers/chromium-1194/chrome-linux/chrome`.
  - The agent proxy needs `testChromeFlags` set to ignore-certificate-errors
    (test hook only).
  - For a visible window, use `xvfb-run`.

## Next ideas (not started)

1. **Deep check.** A lightweight Windows .NET test bench that runs a mod's
   code with Harmony hooks plus an OS sandbox. Plan: `docs/deep-check.md`.
   The first prototype runs the Pennywise mod (01 STUDIO).
2. Check that the Skymods window tick works on the owner's PC; fix
   whatever it turns up.
3. Light/dark theme exists (default dark). The owner had more feature ideas
   to pick from.
