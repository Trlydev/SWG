//go:build cgo && !nofyne

package gui

import (
	"fmt"
	"image/color"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trlydev/SWG/internal/app"
	"github.com/Trlydev/SWG/internal/desktop"
	"github.com/Trlydev/SWG/internal/game"
)

// The native window (Fyne). It shows the same things as the web page in
// web/ and talks to the same API, in-process (server.call).

func init() { nativeUI = runNative }

// stateView is /api/state.
type stateView struct {
	NXMOffer     *nxmOffer       `json:"nxm_offer"`
	PackOffer    *packOffer      `json:"pack_offer"`
	Nexus        nexusView       `json:"nexus"`
	Version      string          `json:"version"`
	Paths        app.Paths       `json:"paths"`
	Loader       game.Loader     `json:"loader"`
	NeedCompiler int             `json:"scripts_need_compiler"`
	Installed    []installedView `json:"installed"`
	Profiles     []string        `json:"profiles"`
	Profile      string          `json:"profile"`
	Job          *job            `json:"job"`
	Queued       int             `json:"queued"`      // installs waiting for the running task
	AutoUpdate   string          `json:"auto_update"` // what the last automatic update did
	Settings     settings        `json:"settings"`
	Skymods      skymodsState    `json:"skymods"`
	REPPG        reppgView       `json:"reppg"`
	WinHTTP      *winhttpState   `json:"winhttp"`
	SteamItems   []string        `json:"steam_items"` // subscribed to on the Steam Workshop
	Cutoff       string          `json:"cutoff"`
	LastBackup   string          `json:"lastBackup"`
	Desktop      struct {
		Installed   bool   `json:"installed"`
		CopyExists  bool   `json:"copy_exists"`
		InstallPath string `json:"install_path"`
		RunningFrom string `json:"running_from"`
		Platform    string `json:"platform"`
	} `json:"desktop"`
}

type ui struct {
	s   *server
	app fyne.App
	win fyne.Window

	mu        sync.Mutex
	st        *stateView
	lastJobID int
	running   bool
	jobLabel  string

	status   *widget.Label
	logText  *widget.Label
	logBox   *container.Scroll
	logLines []string
	logNext  int
	busy     []*widget.Button // disabled while a task runs

	updateBanner  *fyne.Container
	updateText    *widget.Label
	installBanner *fyne.Container
	installText   *widget.Label
	protonBanner  *fyne.Container
	body          *fyne.Container // the page with the notices above it
	installDesk   *widget.Check

	gameChipText *canvas.Text
	gameChipBg   *canvas.Rectangle
	versionChip  *canvas.Text
	safetyCut    *widget.Label
	safetyCool   *widget.Label
	navs         []*navItem
	views        []fyne.CanvasObject
	viewStack    *fyne.Container
	dot          *canvas.Circle
	dotAnim      *fyne.Animation
	dotBox       *fyne.Container
	logWrap      *fyne.Container

	browse    *browseView
	installed *installedPane
	recover   *recoverView
	packs     *modpacksView

	detailHolder *fyne.Container // the details panel beside the page
	set          *settingsView
	details      *detailsWin

	installedSig string
	nexusSig     string
	nxmShownAt   time.Time
	packShownAt  time.Time
	thumbs       *thumbLoader
	page         int               // the page shown (show)
	sysVariant   fyne.ThemeVariant // the system's light/dark when last built
}

func runNative(s *server) error {
	a := fyneapp.NewWithID("io.github.dogekingc.ppgmods")
	a.SetIcon(fyne.NewStaticResource("icon.png", desktop.IconPNG()))
	windowTheme = themeMode
	a.Settings().SetTheme(newTheme())
	u := &ui{s: s, app: a}
	u.thumbs = newThumbLoader(u)
	u.win = a.NewWindow(desktop.AppName)
	u.win.Resize(fyne.NewSize(1320, 840))
	u.win.SetMaster()
	u.win.SetCloseIntercept(func() {
		u.mu.Lock()
		running := u.running
		u.mu.Unlock()
		if !running {
			u.win.Close()
			return
		}
		u.confirm("A task is still running", text("Closing now stops it halfway (an install or update could be left incomplete). Quit anyway?"),
			"Quit anyway", true, func() { u.win.Close() })
	})
	u.build()
	u.followSystem()
	go func() {
		for {
			select {
			case <-s.quit:
				fyne.Do(a.Quit)
				return
			case <-s.show:
				fyne.Do(func() { u.win.Show(); u.win.RequestFocus() })
			}
		}
	}()
	go u.loop()
	u.win.ShowAndRun()
	return nil
}

func (u *ui) build() {
	p := pal()
	// footer: task status with a pulsing dot, and the log
	u.status = widget.NewLabel("Ready")
	u.status.Truncation = fyne.TextTruncateEllipsis
	u.status.Importance = widget.LowImportance
	u.status.SizeName = sSmall
	u.dot, u.dotAnim = pulseDot()
	u.dotBox = container.NewCenter(fixed(u.dot, 9, 9))
	u.dotBox.Hide()
	u.logText = widget.NewLabel("")
	u.logText.TextStyle = fyne.TextStyle{Monospace: true}
	u.logText.SizeName = sSmall
	u.logText.Wrapping = fyne.TextWrapBreak
	logBg := canvas.NewRectangle(p.bg)
	u.logBox = container.NewScroll(u.logText)
	u.logBox.SetMinSize(fyne.NewSize(0, 190))
	u.logWrap = container.NewStack(logBg, container.NewBorder(hline(), nil, nil, nil, u.logBox))
	u.logWrap.Hide()
	logBtn := widget.NewButton("Show log", nil)
	logBtn.Importance = widget.LowImportance
	logBtn.OnTapped = func() {
		if u.logWrap.Visible() {
			u.logWrap.Hide()
			logBtn.SetText("Show log")
		} else {
			u.logWrap.Show()
			u.logBox.ScrollToBottom()
			logBtn.SetText("Hide log")
		}
	}
	footBg := canvas.NewRectangle(p.surface)
	footRow := container.New(layout.NewCustomPaddedLayout(0, 0, 12, 8),
		container.NewBorder(nil, nil, u.dotBox, logBtn, u.status))
	footer := container.NewStack(footBg, container.NewBorder(hline(), nil, nil, nil, container.NewVBox(footRow, u.logWrap)))

	// notices: one slim line each, above the page (update, app install)
	u.updateText = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	u.updateText.Truncation = fyne.TextTruncateEllipsis
	upd := widget.NewButton("Update now", func() { u.run(map[string]any{"action": "self-update"}, "Updating ppgmods") })
	upd.Importance = widget.HighImportance
	u.updateBanner = notice(container.NewBorder(nil, nil, widget.NewIcon(theme.DownloadIcon()), upd, u.updateText), p.warnBg)
	u.updateBanner.Hide()

	u.installText = widget.NewLabel("")
	u.installText.Truncation = fyne.TextTruncateEllipsis
	u.installDesk = widget.NewCheck("desktop shortcut", nil)
	u.installDesk.SetChecked(true)
	inst := widget.NewButton("Install", u.installApp)
	inst.Importance = widget.HighImportance
	later := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		u.app.Preferences().SetBool("install-dismissed", true)
		u.setNotice(u.installBanner, false)
	})
	later.Importance = widget.LowImportance
	u.installBanner = notice(container.NewBorder(nil, nil, widget.NewIcon(theme.ComputerIcon()), container.NewHBox(u.installDesk, inst, later), u.installText), p.surface2)
	u.installBanner.Hide()

	// Proton won't load BepInEx (and so RE_PPG) without the winhttp override.
	protonText := widget.NewLabelWithStyle("Proton isn't set to load RE_PPG, so your C# mods won't run.", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	protonText.Truncation = fyne.TextTruncateEllipsis
	protonFix := widget.NewButton("Fix it", u.fixProton)
	protonFix.Importance = widget.HighImportance
	u.protonBanner = notice(container.NewBorder(nil, nil, widget.NewIcon(theme.WarningIcon()), protonFix, protonText), p.warnBg)
	u.protonBanner.Hide()

	// the pages
	u.browse = newBrowse(u)
	u.installed = newInstalled(u)
	u.packs = newModpacks(u)
	u.recover = newRecover(u)
	u.set = newSettings(u)
	u.views = []fyne.CanvasObject{u.browse.root, u.installed.root, u.packs.root, u.recover.root, safetyView(u), u.set.root}
	for i, v := range u.views {
		if i > 0 {
			v.Hide()
		}
	}
	u.viewStack = container.NewStack(u.views...)

	// the sidebar: brand, Play, the pages, and the game's status at the foot
	icon := canvas.NewImageFromResource(fyne.NewStaticResource("icon.png", desktop.IconPNG()))
	icon.SetMinSize(fyne.NewSize(34, 34))
	icon.FillMode = canvas.ImageFillContain
	name := canvas.NewText("PPG Mod Manager", p.text)
	name.TextStyle = fyne.TextStyle{Bold: true}
	name.TextSize = 15
	sub := canvas.NewText("People Playground", p.muted)
	sub.TextSize = 11
	brand := container.NewHBox(container.NewCenter(icon), container.NewCenter(container.New(&vlist{gap: 0}, name, sub)))
	play := widget.NewButtonWithIcon("Play", theme.MediaPlayIcon(), u.play)
	play.Importance = widget.HighImportance
	items := []struct {
		icon fyne.Resource
		name string
	}{
		{theme.SearchIcon(), "Browse"}, {theme.ListIcon(), "My mods"}, {theme.ContentCopyIcon(), "Modpacks"},
		{theme.HistoryIcon(), "Recover Workshop"}, {theme.WarningIcon(), "Safety"}, {theme.SettingsIcon(), "Settings"},
	}
	main, tools := tight(), tight()
	for i, it := range items {
		i := i
		n := newNavItem(it.icon, it.name, func() { u.show(i) })
		u.navs = append(u.navs, n)
		if i < pageRecover {
			main.Add(n.root)
		} else {
			tools.Add(n.root)
		}
	}
	u.navs[pageBrowse].setActive(true)
	toolsLabel := muted("MORE")
	toolsLabel.TextStyle = fyne.TextStyle{Bold: true}
	toolsLabel.SizeName = sTiny
	var game *fyne.Container
	game, u.gameChipText, u.gameChipBg = chip("Checking game…", p.surface2, p.text, true)
	gameTap := newTapArea(game, func() {
		if u.state().Paths.Game == "" {
			u.show(pageSettings)
		}
	}, nil)
	u.versionChip = canvas.NewText("ppgmods", p.muted)
	u.versionChip.TextSize = 11
	repo := widget.NewHyperlink("GitHub", nil)
	repo.OnTapped = func() { u.openURL("https://github.com/Trlydev/SWG") }
	repo.SizeName = sSmall
	quit := widget.NewButtonWithIcon("", theme.LogoutIcon(), func() { u.s.stop("") })
	quit.Importance = widget.LowImportance
	foot := container.New(&vlist{gap: 6}, container.NewHBox(gameTap),
		container.NewBorder(nil, nil, container.NewCenter(u.versionChip), quit, container.NewCenter(repo)))
	sideBg := canvas.NewRectangle(p.surface)
	side := container.NewStack(sideBg, container.NewBorder(nil, nil, nil, vline(),
		container.New(layout.NewCustomPaddedLayout(14, 10, 12, 12),
			container.NewBorder(container.New(&vlist{gap: 14}, brand, play), foot, nil, nil,
				container.New(&vlist{gap: 10}, main, container.New(layout.NewCustomPaddedLayout(6, 0, 8, 0), toolsLabel), tools)))))
	sideFixed := container.New(&widthLayout{224}, side)

	// details open beside the page, not over it
	// (the fixed-width column itself is shown and hidden, so a closed panel
	// takes no room)
	u.detailHolder = container.New(&widthLayout{430})
	u.detailHolder.Hide()
	u.body = container.NewBorder(container.NewVBox(u.protonBanner, u.updateBanner, u.installBanner), nil, nil,
		u.detailHolder, u.viewStack)
	u.win.SetContent(container.NewBorder(nil, footer, sideFixed, nil, u.body))
	u.win.Canvas().SetOnTypedKey(func(k *fyne.KeyEvent) {
		if k.Name == fyne.KeyEscape && u.details != nil {
			u.details.close()
		}
	})
}

// retheme switches the window to another Appearance at once. Many parts
// take their colours when they're built, so the window is built again in
// the new colours, on the Settings page where the change was made.
func (u *ui) retheme(mode string) {
	u.rebuildIn(mode)
	go func() {
		u.refreshState()
		u.browse.search("", 1)
		u.checkRelease(false)
	}()
}

// followSystem rebuilds the window when the system switches between light
// and dark while Appearance is "Match system": Fyne's own widgets follow
// by themselves, but much of the window takes its colours when built and
// was left in the old ones.
func (u *ui) followSystem() {
	u.sysVariant = u.app.Settings().ThemeVariant()
	ch := make(chan fyne.Settings, 1)
	u.app.Settings().AddChangeListener(ch)
	go func() {
		for st := range ch {
			v := st.ThemeVariant()
			fyne.Do(func() {
				if u.systemChanged(v) {
					go u.refreshState()
				}
			})
		}
	}()
}

// systemChanged notes the system's light/dark variant and rebuilds the
// window, on the open page, if it changed under "Match system".
func (u *ui) systemChanged(v fyne.ThemeVariant) bool {
	if v == u.sysVariant {
		return false
	}
	u.sysVariant = v
	if windowTheme != "system" {
		return false
	}
	u.rebuildOn(windowTheme, u.page)
	return true
}

// rebuildIn builds the window again in mode's colours, on Settings (where
// Appearance is changed).
func (u *ui) rebuildIn(mode string) { u.rebuildOn(mode, pageSettings) }

// rebuildOn builds the window again in mode's colours, showing page.
func (u *ui) rebuildOn(mode string, page int) {
	windowTheme = mode
	u.app.Settings().SetTheme(newTheme())
	if u.details != nil {
		u.details.close()
		u.details = nil
	}
	u.navs, u.busy = nil, nil
	u.installedSig, u.nexusSig = "", ""
	u.thumbs.forget()
	u.build()
	u.show(page)
}

// The pages, in sidebar order.
const (
	pageBrowse = iota
	pageMine
	pageModpacks
	pageRecover
	pageSafety
	pageSettings
)

// show switches the sidebar section.
func (u *ui) show(i int) {
	u.page = i
	for j, v := range u.views {
		if j == i {
			v.Show()
		} else {
			v.Hide()
		}
		u.navs[j].setActive(j == i)
	}
	if i == pageMine || i == pageModpacks {
		go u.refreshState()
	}
}

// play starts People Playground through Steam.
func (u *ui) play() {
	if u.state().Paths.Game == "" {
		u.toast("People Playground wasn't found: set its folder in Settings")
		u.show(pageSettings)
		return
	}
	go func() {
		if err := u.s.call("GET", "/api/open?what=play", nil, nil); err != nil {
			u.toast(err.Error())
			return
		}
		u.toast("Starting People Playground through Steam")
	}()
}

// setNotice shows or hides a notice line and lays the page out again: the
// row of notices above the page doesn't grow or shrink by itself, so a
// notice shown later (once the state or the release check is in) was drawn
// over the page header, without its background.
func (u *ui) setNotice(n *fyne.Container, on bool) {
	if n.Visible() == on {
		return
	}
	if on {
		n.Show()
	} else {
		n.Hide()
	}
	if u.body != nil {
		u.body.Refresh()
	}
}

// notice is a slim one-line strip above the page.
func notice(content fyne.CanvasObject, fill color.Color) *fyne.Container {
	bg := canvas.NewRectangle(fill)
	return container.NewStack(bg, container.NewBorder(nil, hline(), nil, nil, container.New(layout.NewCustomPaddedLayout(2, 2, 14, 10), content)))
}

// view wraps a section's content like the page's main area.
func view(content fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVScroll(container.New(layout.NewCustomPaddedLayout(22, 30, 26, 26), content))
}

type widthLayout struct{ w float32 }

func (l *widthLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	if len(objs) == 0 {
		return fyne.NewSize(l.w, 0)
	}
	return fyne.NewSize(l.w, objs[0].MinSize().Height)
}

func (l *widthLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Resize(size)
	}
}

func vline() fyne.CanvasObject {
	r := canvas.NewRectangle(pal().border)
	r.SetMinSize(fyne.NewSize(1, 1))
	return r
}

func banner(content fyne.CanvasObject, fill color.Color) *fyne.Container {
	bg := canvas.NewRectangle(fill)
	return container.NewStack(bg, container.NewBorder(nil, hline(), nil, nil, container.New(layout.NewCustomPaddedLayout(4, 4, 16, 16), content)))
}

// loop polls the log, the running task and the state, like the web page.
func (u *ui) loop() {
	u.refreshState()
	go u.browse.search("", 1)
	go u.checkRelease(false)
	tick := time.NewTicker(800 * time.Millisecond)
	defer tick.Stop()
	n := 0
	for {
		select {
		case <-u.s.quit:
			return
		case <-tick.C:
		}
		n++
		u.pollLog()
		u.pollJob()
		u.mu.Lock()
		running := u.running
		u.mu.Unlock()
		if n%5 == 0 && !running {
			u.refreshState()
		}
		if n%2250 == 0 { // about every 30 minutes
			u.checkRelease(false)
		}
	}
}

func (u *ui) refreshState() {
	var st stateView
	if err := u.s.call("GET", "/api/state", nil, &st); err != nil {
		return
	}
	u.mu.Lock()
	u.st = &st
	if st.Job != nil && st.Job.Running && !st.Job.Background && u.lastJobID == 0 {
		u.lastJobID, u.jobLabel = st.Job.ID, "" // e.g. queued installs, started by the server
	}
	follow := u.lastJobID == 0
	u.mu.Unlock()
	sig := ""
	for _, m := range st.Installed {
		sig += fmt.Sprint(m.Key, m.Name, m.Missing, m.Folders, m.ScanMax, m.Pinned, m.RiskAccepted, m.Author, m.Withdrawn, "|")
	}
	fyne.Do(func() {
		p := pal()
		if st.Paths.Game != "" {
			u.gameChipText.Text, u.gameChipText.Color, u.gameChipBg.FillColor = "● People Playground found", p.ok, p.okBg
		} else {
			u.gameChipText.Text, u.gameChipText.Color, u.gameChipBg.FillColor = "Game not found: set folder", p.bad, p.badBg
		}
		u.gameChipBg.StrokeWidth = 0
		u.gameChipText.Refresh()
		u.gameChipBg.Refresh()
		u.versionChip.Text = "ppgmods " + st.Version
		u.versionChip.Refresh()
		u.navs[pageMine].setCount(fmt.Sprint(len(st.Installed)))
		sig += fmt.Sprint(st.NeedCompiler)
		if sig != u.installedSig {
			u.installedSig = sig
			u.installed.render()
			u.browse.markInstalled()
		}
		u.recover.update(&st)
		u.packs.update(&st)
		u.set.update(&st)
		u.renderDesktop(&st)
		u.setNotice(u.protonBanner, st.WinHTTP != nil && !st.WinHTTP.OK)
		u.renderSkymods(st.Skymods)
		if (st.Job == nil || !st.Job.Running) && st.Queued == 0 {
			u.browse.resetQueued()
		}
		u.installed.setAuto(&st)
		u.showNXMOffer(st.NXMOffer)
		u.showPackOffer(st.PackOffer)
		if follow {
			u.setJob(st.Job, "")
		}
	})
}

func (u *ui) state() *stateView {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.st == nil {
		return &stateView{}
	}
	return u.st
}

// installedKeys is every ref an installed item answers to.
func (u *ui) installedKeys() map[string]bool {
	keys := map[string]bool{}
	st := u.state()
	for _, m := range st.Installed {
		keys[m.Key] = true
		for _, a := range m.Aliases {
			keys[a] = true
		}
	}
	for _, id := range st.SteamItems {
		keys["sky:"+id] = true // subscribed to on Steam: in the game already
	}
	return keys
}

// ---------- tasks ----------

func (u *ui) run(req map[string]any, label string) {
	go func() {
		var j job
		if err := u.s.call("POST", "/api/action", req, &j); err != nil {
			u.toast(err.Error())
			return
		}
		if j.ID == 0 && j.Queued > 0 {
			u.toast(fmt.Sprintf("Added to the queue (%d waiting): it installs when the current task ends", j.Queued))
			return
		}
		u.mu.Lock()
		u.lastJobID, u.jobLabel = j.ID, label
		u.mu.Unlock()
		fyne.Do(func() { u.setJob(&j, label) })
	}()
}

func (u *ui) install(ref, name string, override map[string]bool, mirror, confirm string) {
	req := map[string]any{"action": "install", "refs": []string{ref}, "mirror": mirror, "confirm": confirm}
	if override != nil {
		req["override"] = override
	}
	if name == "" {
		name = ref
	}
	u.run(req, "Installing "+name)
}

func (u *ui) setJob(j *job, label string) {
	if j == nil || j.Background && !j.Running {
		u.status.SetText("Ready")
		u.setRunning(false)
		u.setBusy(false)
		return
	}
	u.mu.Lock()
	u.running = j.Running
	u.mu.Unlock()
	u.setBusy(j.Running)
	if j.Running {
		if label == "" {
			label = j.Name
		}
		if label == "install" {
			label = "Installing"
		}
		if j.Background && j.Name == "update" {
			label = "Updating subscribed mods"
		}
		u.status.SetText(label + "…")
		u.setRunning(true)
		return
	}
	u.setRunning(false)
	if j.OK {
		u.status.SetText("Last task: " + j.Name + " finished")
	} else {
		u.status.SetText("Last task: " + j.Name + " " + orStr(j.Error, "failed"))
	}
}

func (u *ui) setRunning(on bool) {
	if on == u.dotBox.Visible() {
		return
	}
	if on {
		u.dotBox.Show()
		u.status.Importance = widget.MediumImportance
		u.dotAnim.Start()
	} else {
		u.dotBox.Hide()
		u.status.Importance = widget.LowImportance
		u.dotAnim.Stop()
	}
	u.status.Refresh()
}

func (u *ui) setBusy(b bool) {
	for _, x := range u.busy {
		if b {
			x.Disable()
		} else {
			x.Enable()
		}
	}
	u.browse.setBusy(b)
}

func (u *ui) busyButton(label string, f func()) *widget.Button {
	b := widget.NewButton(label, f)
	u.busy = append(u.busy, b)
	return b
}

func (u *ui) pollJob() {
	u.mu.Lock()
	id, label := u.lastJobID, u.jobLabel
	u.mu.Unlock()
	if id == 0 {
		return
	}
	var j *job
	if u.s.call("GET", "/api/job", nil, &j) != nil || j == nil || j.ID != id {
		return
	}
	if j.Running {
		return
	}
	u.mu.Lock()
	u.lastJobID, u.running = 0, false
	u.mu.Unlock()
	fyne.Do(func() {
		u.setJob(j, label)
		u.jobDone(j)
	})
	u.refreshState()
}

func (u *ui) pollLog() {
	var r struct {
		Lines []string `json:"lines"`
		Next  int      `json:"next"`
	}
	if u.s.call("GET", fmt.Sprintf("/api/log?since=%d", u.logNext), nil, &r) != nil {
		return
	}
	u.logNext = r.Next
	if len(r.Lines) == 0 {
		return
	}
	u.logLines = append(u.logLines, r.Lines...)
	if len(u.logLines) > 600 {
		u.logLines = u.logLines[len(u.logLines)-500:]
	}
	text := strings.Join(u.logLines, "\n")
	last := r.Lines[len(r.Lines)-1]
	u.mu.Lock()
	running := u.running
	u.mu.Unlock()
	fyne.Do(func() {
		u.logText.SetText(text)
		if u.logWrap.Visible() {
			u.logBox.ScrollToBottom()
		}
		if running && len(last) > 9 {
			u.status.SetText(last[9:])
		}
	})
}

// jobDone reports a finished task, as the web page's jobDone does.
func (u *ui) jobDone(j *job) {
	sum := j.Summary
	if j.Name == "verify" {
		u.installed.showVerify(j.Problems)
		u.show(pageMine)
		return
	}
	if j.OK {
		switch {
		case j.Data["restart"] != "":
			u.toast("Updated to " + j.Data["version"] + ". Restarting…")
			u.restart("")
		case j.Name == "find-installed":
			if j.Data["found"] != "" && j.Data["found"] != "0" {
				u.toast("Now tracking " + j.Data["found"] + " item(s) that were already installed")
			} else {
				u.toast("No untracked mods found")
			}
		case j.Name == "install-app":
			msg := "PPG Mod Manager is installed at " + j.Data["exe"] + ". Start it from the Start menu, your application menu or the desktop shortcut. You can delete the file you downloaded."
			if u.state().Desktop.Installed {
				u.message("Installed", msg)
			} else {
				u.confirm("Installed", text(msg), "Switch to the installed copy", false, func() { u.restart("installed") })
			}
		case sum != nil && j.Name == "update":
			verb := "can update"
			if u.installed.lastApply {
				verb = "updated"
			}
			msg := fmt.Sprintf("%d up to date, %d %s, %d held back", sum.Current, sum.OK, verb, sum.Refused)
			if sum.Manual > 0 {
				msg += fmt.Sprintf(", %d to update by hand on Nexus Mods", sum.Manual)
			}
			if sum.NotChecked > 0 {
				msg += fmt.Sprintf(", %d with nothing to check (Show log says why)", sum.NotChecked)
			}
			u.toast(msg)
		case sum != nil && j.Name == "repair":
			u.toast(fmt.Sprintf("%d restored, %d refused, %d could not be restored (see the log)", sum.OK, sum.Refused, sum.Failed))
		case sum != nil:
			u.toast(fmt.Sprintf("%d installed, %d refused, %d errors", sum.OK, sum.Refused, sum.Failed))
		case j.Name == "backup" && j.Data != nil:
			u.recover.path.SetText(j.Data["dest"])
			u.toast("Backup saved. Now restore the safe copies (step 2).")
		case j.Name == "install":
			what := "mods"
			dir := j.Data["mods_dir"]
			if j.Data["kind"] == "contraption" {
				what, dir = "contraptions", j.Data["contraptions_dir"]
			}
			u.toastAction("Installed into "+orStr(dir, "your "+what+" folder"), "Open folder", func() { u.open(what) })
		case j.Name == "ow-share" && j.Data != nil:
			u.showShare(j.Data)
		case j.Name == "reppg-install" && j.Data["version"] != "":
			u.toast("RE_PPG " + j.Data["version"] + " is installed. Start People Playground once so it sets itself up; C# mods then work again.")
		case j.Name == "revoke-approvals" && j.Data != nil:
			u.toast("Revoked " + j.Data["revoked"] + " Trust and run approval(s); RE_PPG asks again before running those mods")
		case j.Name == "list-export" && j.Data != nil:
			u.showPackCode(j.Data)
		case j.Name == "remove" && j.Data["others"] != "":
			u.toastAction("Removed. Another copy is still in your Mods folder ("+j.Data["others"]+"), not installed by ppgmods.", "Open Mods folder", func() { u.open("mods") })
		default:
			u.toast(j.Name + " finished")
		}
		return
	}
	body := container.NewVBox()
	if len(j.Reasons) > 0 {
		body.Add(text("ppgmods did not install this mod:"))
		for _, r := range j.Reasons {
			body.Add(text("• " + stripOverride(r)))
		}
	} else {
		body.Add(text(orStr(j.Error, "Something went wrong.")))
	}
	if len(j.Findings) > 0 {
		body.Add(small("What the scanner found:"))
		body.Add(mono(strings.Join(j.Findings, "\n")))
	} else {
		body.Add(small("The log at the bottom has the full details."))
	}
	if j.Browser != nil && j.Browser.NXM {
		u.confirm("Get it from Nexus Mods", text("On the mod's Files tab, click Mod Manager Download. ppgmods asks you to confirm, then downloads, scans and installs it."),
			"Open Files tab", false, func() { u.openURL(j.Browser.URL) })
		return
	}
	if j.Steam != nil {
		u.steamDialog(j.Steam, j.Retry)
		return
	}
	if j.Browser != nil && j.Retry != nil {
		b := container.NewVBox(text("This copy has to be downloaded in your browser: " + j.Browser.Reason + "."))
		if strings.HasPrefix(j.Browser.Mirror, "01studio:") {
			b.Add(small("No 01studio.dev account? Many 01 STUDIO mods are also on Nexus Mods, where a free account works: open the mod's details and pick its Nexus Mods or mirror copy instead."))
		}
		if strings.HasPrefix(j.Browser.Mirror, "nexus:") {
			b.Add(nexusAccountBox(u))
		}
		if j.Browser.Handoff {
			b.Add(text("Open the download page and click Slow download: Nexus Mods hands the file straight to ppgmods, which scans and installs it. (If your browser asks whether to open the link with ppgmods, allow it.)"))
		} else {
			b.Add(text("Open the download page, click its download button, and ppgmods will pick the file up from your Downloads folder and install it automatically."))
		}
		u.confirm("Download in your browser", b, "Open download page", false, func() { u.run(j.Retry, "Waiting for the browser download") })
		return
	}
	title := "Task failed"
	if j.Error == "refused" {
		title = "Not installed"
	}
	switch {
	case j.Retry != nil && j.Risk:
		name := "this mod"
		if refs, ok := j.Retry["refs"].([]any); ok && len(refs) > 0 {
			name = fmt.Sprint(refs[0])
		}
		u.confirm(title, body, "Accept the risk…", true, func() {
			u.riskDialog(name, j.Findings, func(phrase string) {
				req := map[string]any{}
				for k, v := range j.Retry {
					req[k] = v
				}
				req["confirm"] = phrase
				u.run(req, "Installing (risk accepted)")
			})
		})
	case j.Retry != nil && j.Retry["action"] == "reppg-install":
		body.Add(text("RE_PPG may not load in this game version, or may fail in ways it would otherwise catch. Usually waiting a day or two for an RE_PPG update is better."))
		u.confirm("RE_PPG doesn't match this game version", body, "Install anyway", true, func() { u.run(j.Retry, "Installing RE_PPG (other game version)") })
	case j.Retry != nil:
		body.Add(warnBox("Only continue if you have read the findings above and trust this mod's author. Mods run with full access to your PC."))
		u.confirm(title, body, "Install anyway", true, func() { u.run(j.Retry, "Installing (override)") })
	default:
		u.message2(title, body)
	}
}

func stripOverride(r string) string {
	if i := strings.Index(r, " (override: "); i >= 0 {
		if k := strings.Index(r[i:], ")"); k >= 0 {
			return r[:i] + r[i+k+1:]
		}
	}
	return r
}

// restart starts ppgmods again (after an update, or the installed copy);
// the new process opens its own window and this one closes.
func (u *ui) restart(which string) {
	go u.s.call("POST", "/api/action", map[string]any{"action": "restart", "key": which}, nil)
}

func (u *ui) installApp() {
	u.run(map[string]any{"action": "install-app", "apply": u.installDesk.Checked}, "Installing PPG Mod Manager")
}

func (u *ui) renderDesktop(st *stateView) {
	d := st.Desktop
	where := "your application menu"
	if d.Platform == "windows" {
		where = "the Start menu"
	}
	if d.Installed || u.app.Preferences().Bool("install-dismissed") {
		u.setNotice(u.installBanner, false)
	} else {
		if d.CopyExists {
			u.installText.SetText("PPG Mod Manager is installed on this PC, but you are running another copy. Install this copy over it, or switch to the installed one.")
		} else {
			u.installText.SetText("Install ppgmods on this PC (" + where + "): it updates itself, and you can delete this download.")
		}
		u.setNotice(u.installBanner, true)
	}
	u.set.renderDesktop(st, where)
}

func (u *ui) checkRelease(force bool) {
	q := ""
	if force {
		q = "?force=1"
	}
	var r struct {
		Current string `json:"current"`
		Latest  *struct {
			Version string `json:"version"`
			Newer   bool   `json:"newer"`
		} `json:"latest"`
		Error string `json:"error"`
	}
	if u.s.call("GET", "/api/release"+q, nil, &r) != nil {
		return
	}
	fyne.Do(func() {
		switch {
		case r.Error != "":
			u.set.about.SetText("Could not check for updates: " + r.Error)
			if force {
				u.toast("Could not check for updates")
			}
		case r.Latest != nil && r.Latest.Newer:
			u.set.about.SetText(r.Latest.Version + " is available.")
			u.updateText.SetText(fmt.Sprintf("ppgmods %s is available (you have %s).", r.Latest.Version, r.Current))
			u.setNotice(u.updateBanner, true)
		case r.Latest != nil:
			u.set.about.SetText("You have the latest version (" + r.Latest.Version + ").")
		}
	})
}

// showNXMOffer asks before installing what a Nexus link sent: any website
// can open such a link, so it never installs by itself.
func (u *ui) showNXMOffer(o *nxmOffer) {
	if o == nil || o.At.Equal(u.nxmShownAt) {
		return
	}
	u.nxmShownAt = o.At
	body := container.NewVBox(text("Nexus Mods sent "+o.Name+" to ppgmods."),
		small("It will be downloaded and scanned like any mod before it is installed. If you didn't just click Mod Manager Download, close this."))
	u.dialog("Install from Nexus Mods?", body, "Install", false, func() {
		u.run(map[string]any{"action": "nxm", "path": o.URL}, "Installing "+o.Name+" from Nexus Mods")
	}, func() { go u.s.call("POST", "/api/nxm-dismiss", map[string]any{}, nil) })
	u.win.RequestFocus()
}

// showPackOffer asks before installing a modpack that was opened or pasted.
func (u *ui) showPackOffer(o *packOffer) {
	if o == nil || o.At.Equal(u.packShownAt) {
		return
	}
	u.packShownAt = o.At
	list := container.New(&vlist{gap: 4})
	if o.Name != "" {
		list.Add(widget.NewLabelWithStyle(o.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	}
	todo := 0
	for _, group := range []struct {
		title       string
		contraption bool
	}{{"Mods", false}, {"Contraptions", true}} {
		var rows []fyne.CanvasObject
		for _, it := range o.Items {
			if it.Contraption != group.contraption {
				continue
			}
			name := orStr(it.Name, it.Ref)
			var tags []fyne.CanvasObject
			if it.Installed {
				tags = append(tags, pill("installed", pOK))
			} else {
				todo++
			}
			if it.Off {
				tags = append(tags, pill("off", pNeutral))
			}
			rows = append(rows, container.NewBorder(nil, nil, nil, container.NewHBox(tags...), widget.NewLabel(name)))
		}
		if len(rows) > 0 {
			list.Add(h2(fmt.Sprintf("%s (%d)", group.title, len(rows))))
			for _, r := range rows {
				list.Add(r)
			}
		}
	}
	list.Add(small("A modpack only names mods; each one is downloaded from its site and scanned like any install. Anything that fails the checks is skipped."))
	action := fmt.Sprintf("Install %d", todo)
	if todo == 0 {
		action = ""
		list.Add(text("Everything in it is already installed."))
	}
	dismiss := func() { go u.s.call("POST", "/api/pack-dismiss", map[string]any{}, nil) }
	u.dialog("Open modpack?", list, action, false, func() {
		dismiss()
		u.run(map[string]any{"action": "list-import", "list": o.Text}, fmt.Sprintf("Installing the modpack (%d)", todo))
	}, dismiss)
	u.win.RequestFocus()
}

// showPackCode shows a saved modpack's code, to copy into a chat.
func (u *ui) showPackCode(d map[string]string) {
	code := widget.NewEntry()
	code.SetText(d["code"])
	code.Wrapping = fyne.TextWrapBreak
	code.MultiLine = true
	code.SetMinRowsVisible(3)
	body := container.NewVBox(
		text("Your "+d["count"]+" item(s) as one code. Paste it into a chat; anyone with ppgmods pastes it into Browse's link box (or opens the .ppgm file) to get the same mods."),
		code,
		container.NewHBox(widget.NewButtonWithIcon("Copy code", theme.ContentCopyIcon(), func() {
			u.win.Clipboard().SetContent(d["code"])
			u.toast("Code copied")
		}), widget.NewButton("Show .ppgm file", func() { u.open("exports") })))
	if d["skipped"] != "" && d["skipped"] != "0" {
		body.Add(small(d["skipped"] + " item(s) found on this PC without a source can't be downloaded again, so they aren't in it."))
	}
	u.message2("Modpack saved", body)
}

func (u *ui) showShare(d map[string]string) {
	zip := d["zip"]
	if i := strings.LastIndexAny(zip, `\/`); i >= 0 {
		zip = zip[i+1:]
	}
	body := container.NewVBox(
		text("ppgmods packed it and filled in the submission form for you:"),
		text("1. Click Open the form (you need a GitHub account)."),
		container.NewHBox(widget.NewLabel("2. Drag "+zip+" into its File box."), widget.NewButton("Show the zip", func() { u.open("share") })),
		text("3. Submit. It's checked automatically and, if it passes, published within minutes; the issue tells you."),
		small("Only share mods you made or have the author's permission to share."))
	u.confirm("Share on the Open Workshop", body, "Open the form", false, func() { u.openURL(d["issue_url"]) })
}

// ---------- helpers ----------

func (u *ui) open(what string) {
	go func() {
		if err := u.s.call("GET", "/api/open?what="+what, nil, nil); err != nil {
			u.toast(err.Error())
		}
	}()
}

func (u *ui) openURL(link string) {
	go func() {
		if err := u.s.call("GET", "/api/open?what=url&url="+urlQuery(link), nil, nil); err != nil {
			u.toast(err.Error())
		}
	}()
}

// steamDialog offers Steam's own copy of a Workshop item: subscribing in
// Steam puts it in the game. retry (when a mirror has a copy too)
// installs that copy instead.
func (u *ui) steamDialog(ns *app.NeedsSteam, retry map[string]any) {
	name := orStr(ns.Title, "This item")
	body := container.NewVBox(text(name + " is on the Steam Workshop, as new as any mirror copy or newer. Subscribe there and Steam adds it to the game."))
	var d dialog.Dialog
	if retry != nil {
		mirror := widget.NewButton("Use a mirror copy instead", func() {
			if d != nil {
				d.Hide()
			}
			u.run(retry, "Installing a mirror copy")
		})
		mirror.Importance = widget.LowImportance
		body.Add(container.NewHBox(mirror))
	}
	d = u.dialog("On the Steam Workshop", body, "Open in Steam", false, func() {
		go func() {
			if err := u.s.call("GET", "/api/open?what=steam-item&id="+urlQuery(ns.WorkshopID), nil, nil); err != nil {
				u.toast("Couldn't open Steam: " + err.Error())
			}
		}()
	}, nil)
}

// installREPPG asks, then installs or updates RE_PPG and BepInEx.
func (u *ui) installREPPG(update bool) {
	title, action := "Install RE_PPG?", "Install"
	if update {
		title, action = "Reinstall or update RE_PPG?", "Install"
	}
	body := container.NewVBox(
		text("ppgmods downloads RE_PPG from its official GitHub releases (and BepInEx, which it runs on, if it's missing) and checks every file against RE_PPG's own signature before writing it to the game folder."),
		warnBox("RE_PPG runs C# mods with full access to your PC, like the game used to. ppgmods' checks still apply to every mod you install here."),
		small("Close People Playground first."))
	u.confirm(title, body, action, false, func() {
		u.run(map[string]any{"action": "reppg-install"}, "Installing RE_PPG")
	})
}

// fixProton explains the winhttp override and offers both ways to set it:
// ppgmods writes it into the game's Proton prefix, or the person adds the
// launch options line in Steam.
func (u *ui) fixProton() {
	st := u.state()
	w := st.WinHTTP
	if w == nil {
		return
	}
	copyBtn := widget.NewButtonWithIcon("Copy launch options", theme.ContentCopyIcon(), func() {
		u.win.Clipboard().SetContent(w.Fix)
		u.toast("Copied: paste it in Steam, People Playground, Properties, Launch options")
	})
	body := container.NewVBox(
		text("RE_PPG starts through BepInEx's winhttp.dll in the game folder. Windows loads it by itself, but Proton loads its own winhttp.dll instead unless told otherwise, so RE_PPG never starts and C# mods do nothing."),
		text("ppgmods can tell Proton to prefer the game's winhttp.dll, in People Playground's Proton prefix (the same setting winecfg makes; a copy of the old registry is kept). Close People Playground first."),
		small("Or do it in Steam yourself: People Playground, Properties, General, Launch options:"),
		mono(w.Fix),
		container.NewHBox(copyBtn))
	if w.LaunchOptions != "" {
		body.Add(small("Your launch options now: " + w.LaunchOptions + " (keep what you have and add the WINEDLLOVERRIDES part before %command%)."))
	}
	u.confirm("Let Proton load RE_PPG", body, "Fix it for me", false, func() {
		u.run(map[string]any{"action": "proton-winhttp"}, "Setting Proton up for RE_PPG")
	})
}

// toast shows a short message in the status bar and as a notification-
// style popup that closes itself.
func (u *ui) toast(msg string) { u.toastAction(msg, "", nil) }

func (u *ui) toastAction(msg, label string, f func()) {
	fyne.Do(func() {
		p := pal()
		t := widget.NewRichText(&widget.TextSegment{Text: msg, Style: widget.RichTextStyle{
			ColorName: theme.ColorNameBackground, TextStyle: fyne.TextStyle{Bold: true}, Inline: true}})
		t.Wrapping = fyne.TextWrapWord
		var pop *widget.PopUp
		row := container.NewBorder(nil, nil, nil, nil, t)
		if f != nil {
			b := widget.NewButton(label, func() { pop.Hide(); f() })
			row = container.NewBorder(nil, nil, nil, container.NewCenter(b), t)
		}
		bg := canvas.NewRectangle(p.text)
		bg.CornerRadius = 10
		box := container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(6, 6, 10, 10), row))
		pop = widget.NewPopUp(box, u.win.Canvas())
		cs := u.win.Canvas().Size()
		w := minF(420, cs.Width-36)
		pop.Resize(fyne.NewSize(w, box.MinSize().Height))
		h := pop.MinSize().Height
		pop.ShowAtPosition(fyne.NewPos(cs.Width-w-18, cs.Height-h-56))
		d := 4 * time.Second
		if f != nil || len(msg) > 60 {
			d = 8 * time.Second
		}
		time.AfterFunc(d, func() { fyne.Do(pop.Hide) })
	})
}

// dialog shows a modal with Close and, optionally, an action button.
func (u *ui) dialog(title string, body fyne.CanvasObject, action string, danger bool, run func(), onClose func()) dialog.Dialog {
	var d dialog.Dialog
	closeBtn := widget.NewButton("Close", func() {
		d.Hide()
		if onClose != nil {
			onClose()
		}
	})
	buttons := container.NewHBox(layout.NewSpacer(), closeBtn)
	if action != "" {
		b := widget.NewButton(action, func() { d.Hide(); run() })
		b.Importance = widget.HighImportance
		if danger {
			b.Importance = widget.DangerImportance
		}
		buttons.Add(b)
	}
	scroll := container.NewVScroll(body)
	scroll.SetMinSize(fyne.NewSize(560, minF(body.MinSize().Height, 420)))
	d = dialog.NewCustomWithoutButtons(title, container.NewBorder(nil, buttons, nil, nil, scroll), u.win)
	d.Show()
	return d
}

func (u *ui) confirm(title string, body fyne.CanvasObject, action string, danger bool, run func()) {
	u.dialog(title, body, action, danger, run, nil)
}

func (u *ui) message(title, msg string) { u.dialog(title, text(msg), "", false, nil, nil) }

func (u *ui) message2(title string, body fyne.CanvasObject) {
	u.dialog(title, body, "", false, nil, nil)
}

// riskDialog asks the person to type RiskPhrase before installing a mod
// with CRITICAL findings. The server refuses the override without it.
func (u *ui) riskDialog(name string, findings []string, goOn func(phrase string)) {
	in := widget.NewEntry()
	body := container.NewVBox(text(name + " has CRITICAL findings: code that could harm your PC or your Steam account if it is malicious."))
	var crit []string
	for _, f := range findings {
		if strings.HasPrefix(f, "[CRITICAL") {
			crit = append(crit, f)
		}
	}
	if len(crit) > 0 {
		body.Add(mono(strings.Join(crit, "\n")))
	}
	body.Add(warnBox("Mods run with full access to your PC. Only continue if you know the author, have read the code, or got the mod from them directly. Findings that match what the worm did can't be accepted here."))
	body.Add(text("Type \"" + RiskPhrase + "\" to install it anyway:"))
	body.Add(in)
	var d dialog.Dialog
	ok := widget.NewButton("Install anyway", func() { d.Hide(); goOn(RiskPhrase) })
	ok.Importance = widget.DangerImportance
	ok.Disable()
	in.OnChanged = func(s string) {
		if strings.TrimSpace(s) == RiskPhrase {
			ok.Enable()
		} else {
			ok.Disable()
		}
	}
	buttons := container.NewHBox(layout.NewSpacer(), widget.NewButton("Cancel", func() { d.Hide() }), ok)
	d = dialog.NewCustomWithoutButtons("Accept the risk?", container.NewBorder(nil, buttons, nil, nil, body), u.win)
	d.Resize(fyne.NewSize(600, 0))
	d.Show()
	u.win.Canvas().Focus(in)
}

func text(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Wrapping = fyne.TextWrapWord
	return l
}

func small(s string) *widget.Label { return muted(s) }

func mono(s string) fyne.CanvasObject {
	l := widget.NewLabel(s)
	l.Wrapping = fyne.TextWrapBreak
	l.TextStyle = fyne.TextStyle{Monospace: true}
	l.SizeName = sSmall
	return tintBox(l, pal().bg)
}

func heading(s string) *widget.Label { return h2(s) }

func warnBox(s string) fyne.CanvasObject {
	p := pal()
	return tinted(s, p.bad, p.badBg, false)
}

type badgeKind = pillKind

const (
	bNeutral = pNeutral
	bOK      = pOK
	bBad     = pBad
	bWarn    = pWarn
)

func badge(s string, k pillKind) fyne.CanvasObject { return pill(s, k) }

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func minF(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
