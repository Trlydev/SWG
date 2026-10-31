# Handoff (after v0.4.2)

State of ppgmods (People Playground mod manager, Go) for the next session.

## Repo and releases

- Repo: github.com/Trlydev/SWG (the git remote DogeKingC/SWG redirects there).
  Go module path: `github.com/Trlydev/SWG`.
- Latest release: **v0.4.2**. `build.yml` publishes v0.4.N on every push to
  main (counted from the v0.4.* tags). Pushes to other branches don't build.
- Work branch: `claude/trusting-babbage-cu3pyj`.

## Rules from the owner

- Push straight to main only when the owner says "push". Otherwise commit
  to the work branch. No PRs.
- Push only when the full `go test ./...` passes.
- Few commits: one per batch of fixes or per real feature.
- Commit messages carry no Claude session links. The
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` line is fine.
- Explain things short and plain.
- 1.0.0 comes later, after 0.4 has run a while on Windows and Linux.

## Releases so far

| Version | What |
|---|---|
| 0.3.0 | Mod profiles, turn mods off/on, export/import mod lists, Open Workshop review queue, RE_PPG approvals check, Workshop copy first for Nexus |
| 0.3.1 | Review fixes; 01 STUDIO CDN downloads (free mods) |
| 0.3.2 | Update counts every item; DLL capability analysis (reads .NET metadata) |
| 0.3.3 | 01 STUDIO mods labelled "01 STUDIO", downloaded only from 01 STUDIO |
| 0.3.4 | Skymods (smods.ru) Cloudflare check: stop the 2-minute loop |
| 0.3.5 | Native window tells the person when Skymods needs them (banner, popup, Settings section) |
| 0.3.6 | Says when RE_PPG can't run because the game is the native Linux version |
| 0.4.0 | Security update, RE_PPG installs, Zen check fix (below) |
| 0.4.1 | RE_PPG Harmony setup (like its installer); Zen BiDi port found reliably |
| 0.4.2 | Reinstall RE_PPG button; fixes from a 5-agent security review; one-time window code; loader files matched by name and release; Popular sorting fixed (all time from the popularity data); thumbnails shrunk and cached on disk |

## What 0.4 has

- **Skymods check opens the page (Zen).** The window used to open on a blank
  tab: Zen keeps tabs of its own, so "the first tab" wasn't the visible one.
  The browser now starts on the page; ppgmods uses the tab showing it, or
  opens one (`cfclear/firefox.go`, `pageTab`). Untested on real Zen.
- **Scanner** (`internal/scan`):
  - `hidden.go`: a program or DLL hidden inside any file (plain, gzip/zlib/
    deflate, XOR-encoded, also inside DLL resources); XOR-hidden strings
    (one pass per needle: XOR of neighbouring bytes cancels the key); C# XOR
    decoding. A recovered DLL is analysed too.
  - `worm.go` `checkEncoded`: hex text decoded like base64.
  - `csharp.go` `joinedStrings`: follows string constants, variables
    assigned once and `string.Concat`.
  - `clr_il.go`: reads method bodies (IL). Findings name the calling method;
    text built with `String.Concat` is checked whole; text handed to a
    reflection lookup is tied to it; XOR-in-a-loop decoding in DLLs.
  - Checked against 41 real mods (same findings as before, plus method
    names) and 2,712 Mono DLLs (no crash, 13 s).
- **RE_PPG install/update** (`internal/reppg`): Settings and the Installed
  tab's C# notice ("Install RE_PPG" / "Update RE_PPG to x"),
  `ppgmods reppg [status | install]`.
  - RE_PPG signs its releases: RSA-3072, PKCS#1 v1.5, SHA-256 over the
    decoded `Payload` of `update.json` and `base-manifest.json` (inside
    `RE_PPG-base.zip`). ppgmods carries the public key; every file is
    checked before any is written; only listed files, only under
    `BepInEx/` and `RE_PPG/`.
  - BepInEx 5.4.23.5 (win x64) is installed when missing, checked against a
    hash ppgmods carries (it matches the loaders index).
  - **Proton (Linux):** nothing extra; the owner confirmed RE_PPG runs
    under Proton without launch options or DLL overrides.
  - The native Linux version is refused (RE_PPG is Windows-only).
- `scan.Hollow` (`hollow.go`): copies a DLL with its native methods given
  empty bodies. Groundwork for a deep check; nothing uses it. The deep
  check's runner was not built (a safety filter stopped that work).

## Findings worth knowing

- **PPG 1.28** (changelog: github.com/studio-minus/people-playground-changelog):
  native Linux, restructured mod loader, C# modding officially removed.
  `ModAPI` still exists; `Modification` is now `SpawnableModification`
  (RE_PPG's `Compat128.dll` brings the old name back). A Lua loader exists
  but is an empty placeholder.
- **RE_PPG** (MIT; the repo has no source, only releases): its runtime
  bundles ZeroOne/ENSYS libraries. ENSYS contains `UnityEngine.SyncCore`,
  which downloads `ENSYS.dll` from `github.com/AlibardaWasTaken/
  BatrixEnergySystem` (address hidden as hex) with no signature check, and
  rewrites it on disk; in mods that bundle ENSYS this runs in `Awake`. Not
  shown to be malicious, but it bypasses RE_PPG's signed updates. The owner
  chose not to contact the developers.
- A Linux build of RE_PPG would need its compiler (`RE_PPG.Compiler.exe`, a
  self-contained win-x64 .NET 10 app; the managed `RE_PPG.Compiler.dll` is
  portable) to start on Linux, plus Linux BepInEx. Proton is the supported
  way for now.

## Security review (0.4.2) — still open

- **Release signing (most important):** self-update only checks
  SHA256SUMS.txt from the same release. Plan: an ed25519 key, private part
  as the GitHub secret RELEASE_SIGNING_KEY, public part built into
  ppgmods; build.yml signs SHA256SUMS.txt, selfupdate verifies it. Also
  give the data jobs (loaders, popularity, archive, workshop) read-only
  tokens with a separate publish step.
- If a v0.5.0 tag is ever pushed by hand, main pushes keep counting 0.4.x.
- Turning a mod back on doesn't notice deleted files.

## Untested, ask the owner

- Skymods: does the check window now show smods.ru in Zen, and does ticking
  the box work?
- RE_PPG: Install from ppgmods (game closed), start the game: does a C#
  mod load?

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
- **Building in the cloud container:** the native (Fyne) window needs
  `apt-get install libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev`;
  test DLLs are built with `mcs` (apt `mono-mcs`).
- **Testing browsers in the cloud container.**
  - Chromium: `/opt/pw-browsers/chromium-1194/chrome-linux/chrome`.
  - The agent proxy needs `testChromeFlags` set to ignore-certificate-errors
    (test hook only).
  - For a visible window, use `xvfb-run`.
