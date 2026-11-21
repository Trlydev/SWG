# Handoff (after v0.5.2)

State of ppgmods (People Playground mod manager, Go) for the next session.

## Repo and releases

- Repo: github.com/Trlydev/SWG (the git remote DogeKingC/SWG redirects there).
  Go module path: `github.com/Trlydev/SWG`.
- Latest release: **v0.5.2**. `build.yml` publishes v0.5.N on every push to
  main (counted from the v0.5.* tags). Pushes to other branches don't build.
- Since 2026-11-20 the work also happens locally in Claude Code on the
  owner's Linux PC (CachyOS, real People Playground under Proton with
  RE_PPG working, Zen Browser as the only browser), so changes can be tried
  on the real game.
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
| 0.4.3 | Signed releases (Ed25519, RELEASE_SIGNING_KEY); Open Workshop thumbnails in the index |
| 0.4.4 | Skymods check: tick the box in a plain browser window (worked for the owner) |
| 0.4.5 | Open Workshop index signed with the original key only (WORKSHOP_SIGNING_KEY); key read however pasted |
| 0.4.6 | Subscribe: tick cards into a cart, Subscribe installs all; installs queue while a task runs; automatic updates (Settings) |
| 0.4.7 | Modpack codes (PPG1., .ppgm, double-click opens them); cards lose their Subscribe button; WorldBox codes recognised |
| 0.4.8 | Windows built on the Linux runner (MinGW); signed checksums name their version (no rollbacks); centred tick box |
| 0.4.9 | Full UI redesign (below); Skymods check background-only; Play button |
| 0.5.0 | RE_PPG: game-version check (signed GameAssemblySha256) and Proton winhttp setup (notice + Fix it, `reppg fix-proton`); faster: Nexus preview failures cached, mirror lookups in parallel, a mod's copies download side by side, modpack mirrors looked up ahead, Skymods check gives up in 8 s when someone waits and is remembered across runs, one background browser kept for Skymods reads (0.2 s a read instead of 0.85 s); notices no longer drawn over the page; Match system follows the desktop; more non-public addresses refused |
| 0.5.1 | New Workshop uploads without code install (code-free post-cutoff copies, after the cooldown); Steam Web API title finds top-mods copies while Skymods is blocked; downloads only stop on a minute of silence (internal/stall); recovered Steam contraptions marked installed and labelled Steam Workshop; Steam Workshop items: live item ranks first (subscribe in Steam when as new as the mirrors); My mods: real icons (thumbnail backfill), search, sort, compact rows; x/image v0.47.0, go 1.26; CI apt fixes |
| 0.5.2 | Steam sign-in by QR code (DepotDownloader 3.4.0, pinned + SHA-256, own loginid, session in <config>/steam): Steam Workshop items download and install by themselves; a sign-in screen over the window on first start ("Continue without Steam"); Steam profile card; Settings in four tabs (Accounts, Game, Mods, App); `steam-login`/`steam-logout`. Windows: the kept Skymods browser joins a kill-on-close job (no browser left behind after a crash); GUI tests race-free (cardGrid's deferred refresh via uiLater); CI caches fall back to older ones after a dependency change; README for Steam, new uploads, My mods |
| 0.4.2 | Reinstall RE_PPG button; fixes from a 5-agent security review; one-time window code; loader files matched by name and release; Popular sorting fixed (all time from the popularity data); thumbnails shrunk and cached on disk |

## The window (0.4.9 redesign)

- Sidebar (`native_fyne.go` build): brand, **Play** (steam://rungameid/1118200
  via `/api/open?what=play`), Browse, My mods, Modpacks, then MORE:
  Recover Workshop, Safety, Settings; game status, version, GitHub, quit at
  the foot. Pages are the `page*` constants.
- Update and app-install notices are slim lines above the page (`notice`).
- Details open in a panel beside the page (`u.detailHolder`, a fixed-width
  column shown/hidden), not a sheet over it.
- Browse: one search box that also takes links, IDs and modpack codes
  (`submit`, `looksLikeRef`); a Trending strip (popular this week, Workshop
  mirrors, GameBanana, Open Workshop) when nothing is searched.
- My mods: Update now + ⋯ (check, verify, find, folders); each row has
  Turn off, ⋯ (page, pin, rollback, share, quarantine) and a remove icon.
- Modpacks page (`native_packs.go`): share, open (code or file), profiles.
- Settings in panels: Game, Mods and updates, Look and speed, Nexus,
  Skymods, Folders, This app, About; Save at the top.
- The `--web` page was not redesigned.

## What 0.4 has

- **Skymods check is background-only (since 0.4.9).** No button, window,
  banner or popup: the invisible (headless) browser passes the check when
  it can; when smods.ru wants a person, Skymods results are just missing
  and `cfclear.KeepSkymodsWorking` retries every 15 minutes. Settings
  shows the status and "Forget the check". Nothing ticks the box for the
  person (that would be bot-check evasion).
- **(old) Skymods check opens the page (Zen).** The window used to open on a blank
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

## Modpacks (since 0.4.7, from WBMM's .wbm)

- `internal/modpack`: a list as one line, `PPG1.` + base64url(deflate(tiny
  binary list)): source (gb/sky/tw/nx/ow), ID (number or slug), contraption,
  off, name. No files or URLs; installing goes through the normal checks.
- Installed tab: "Share as modpack" saves a `.ppgm` file and shows the code
  (Copy code); "Open modpack…" and Browse's link box take a code or file
  and show what it would install before anything happens.
- `.ppgm` opens in ppgmods on a double-click (registered by install-app,
  and on start for installed copies from before). `ppgmods file.ppgm`
  hands it to the open window (`/api/pack-offer`). Old JSON lists still
  import.

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
- Turning a mod back on doesn't notice deleted files.

## What 0.5 adds (details)

- **RE_PPG official installer, tested under Proton** (v0.2.36): it has a
  CLI (`--install --game DIR --package --bepinex-zip --skip-bepinex
  --ignore-game-hash --data`, also `--inspect`, `--uninstall`,
  `--restore-backup`), output only through Proton's own `files/bin/wine`.
  Under Wine it refuses every path through a link ("Updater paths must not
  contain links: Z:\"), i.e. any Linux path; only the prefix's drive_c
  works. ppgmods' install gives byte-identical files (663), so on Linux
  ppgmods is the way to install it.
- **Proton needs the winhttp override** or BepInEx never loads (Wine uses
  its builtin winhttp.dll). `internal/game/proton.go` reads Steam's
  launch options (localconfig.vdf of the most recent account; newer Steam
  has only `timestamp` in loginusers.vdf, no MostRecent) and the prefix's
  user.reg DllOverrides; Fix it adds `"winhttp"="native,builtin"` to
  user.reg (backup kept; refused while that prefix's Wine runs — checked
  via /proc). protonfixes has no entry for People Playground.
- **Skymods check:** a check that wants a person shows the same page
  forever (no way to tell earlier); waited-on reads give up after 8 s
  (`readPersonAfter`), the background retry keeps 25 s; attempts saved in
  browser-checks.json. The owner asked for a custom/minimal/Rust browser
  to pass it faster: declined (that's bot-check evasion). The window app
  keeps one browser (`cfclear.KeepBrowser`, idle 2 min, Pdeathsig on Linux);
  page polls every 150 ms (700 ms was most of a read).
- **Nexus previews** moved to bucket production-nexus-files-s3-meta and
  answer 404 for every PPG file, Nexus's own button included (Nov 2026).
  Failures cached 6 h.
- govulncheck v1.1.4 crashes on Go 1.27; `golang.org/x/image` (v0.24.0,
  decodes thumbnails from archives) is worth updating.

## Untested, ask the owner

- Skymods: does the check window now show smods.ru in Zen, and does ticking
  the box work?
- RE_PPG: Install from ppgmods (game closed), start the game: does a C#
  mod load?
- Proton Fix it on the real game: remove the launch options, press Fix it,
  start the game: does RE_PPG load? (Checked only with Wine's `reg query`.)
- Steam route on the real game: Install on a live item opens Steam's
  page; after subscribing, does the item appear in the game and get the
  installed mark in Browse (within ~10 minutes)?

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
