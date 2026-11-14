//go:build gui

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// h3mod-gui: the same mod manager in a window, for people who'd rather not
// use a terminal. It keeps its h3mod.json next to the program (or in the
// user config folder when that isn't writable), so unpacking the release
// into the HITMAN 3 folder and starting it is all the setup there is.

const guiUsage = `usage: h3mod-gui [--config h3mod.json] [--game dir] [--mods dir]
       h3mod-gui [--config h3mod.json] <command> [args]   (like h3mod; see h3mod-gui help)

  --config  config file (default: h3mod.json next to the program)
  --game    HITMAN 3 folder; saved to the config
  --mods    folder of unpacked mods; saved to the config
`

func main() {
	if hasCommand(os.Args[1:]) {
		attachConsole()
		defaultConfig = guiConfigPath() // the same mods as the window
		cliMain("h3mod-gui")
		return
	}
	fl := flag.NewFlagSet("h3mod-gui", flag.ContinueOnError)
	var out bytes.Buffer
	fl.SetOutput(&out)
	fl.Usage = func() { out.WriteString(guiUsage) }
	cfgPath := fl.String("config", guiConfigPath(), "")
	game := fl.String("game", "", "")
	mods := fl.String("mods", "", "")
	flagErr := fl.Parse(os.Args[1:])

	if flagErr != nil {
		code := 2
		if errors.Is(flagErr, flag.ErrHelp) {
			code = 0
		}
		if attachConsole() {
			fmt.Fprint(os.Stderr, out.String())
			os.Exit(code)
		}
		// no terminal at all (e.g. a Windows shortcut): show it in a window
		a := app.NewWithID("io.github.jim-ww.h3mod")
		w := a.NewWindow("h3mod-gui")
		w.SetContent(container.NewPadded(widget.NewLabelWithStyle(out.String(), fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})))
		w.ShowAndRun()
		os.Exit(code)
	}

	a := app.NewWithID("io.github.jim-ww.h3mod")
	g := &gui{app: a, win: a.NewWindow("h3mod " + version), ctx: context.Background(), state: &State{}, dragFrom: -1, dragTo: -1}
	g.win.Resize(fyne.NewSize(1000, 680))
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		g.win.SetContent(widget.NewLabel(err.Error()))
		g.win.ShowAndRun()
		os.Exit(1)
	}
	g.cfg = cfg
	for _, f := range []struct {
		val string
		dst *string
	}{{*game, &cfg.Game}, {*mods, &cfg.Mods}} {
		if f.val != "" {
			abs, _ := filepath.Abs(f.val)
			*f.dst = abs
		}
	}
	if *game != "" || *mods != "" {
		cfg.fresh = false // folders given: nothing to offer
		if err := cfg.save(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
	}
	openRunLog(cfg, "h3mod-gui "+version)
	g.build()
	g.win.Show()
	g.ensureGame(func() {
		g.firstRun(func() {
			g.reload()
			if g.cfg.CheckUpdates {
				g.checkUpdates(false)
			}
		})
	})
	a.Run()
}

// guiConfigPath is h3mod.json next to the program, or in the user config
// folder when the program's folder isn't writable (e.g. installed by nix).
func guiConfigPath() string {
	dir := exeDir()
	if f, err := os.CreateTemp(dir, ".h3mod-write-test-"); err == nil {
		f.Close()
		os.Remove(f.Name())
		return filepath.Join(dir, "h3mod.json")
	}
	if cd, err := os.UserConfigDir(); err == nil {
		dir = filepath.Join(cd, "h3mod")
		os.MkdirAll(dir, 0o755)
	}
	return filepath.Join(dir, "h3mod.json")
}

type gui struct {
	app   fyne.App
	win   fyne.Window
	ctx   context.Context
	cfg   *Config
	mods  []*Mod // in status order: enabled in load order, then disabled
	sel   *Mod
	state *State // the last deploy: what's in the game

	dragFrom, dragTo int                    // list rows of a drag in progress, -1 otherwise
	updates          map[string]foundUpdate // newer versions found, by mod folder

	list      *widget.List
	details   *fyne.Container
	gameLabel *widget.Label
	modsLabel *widget.Label
	logText   *widget.Label
	logScroll *container.Scroll
	status    *widget.Label // current step, or a drag hint
	summary   *widget.Label // what's deployed and what isn't yet
	working   *widget.ProgressBarInfinite
	progress  *widget.ProgressBar
	actions   []fyne.Disableable
}

func (g *gui) build() {
	g.gameLabel = widget.NewLabel("")
	g.modsLabel = widget.NewLabel("")
	g.gameLabel.Truncation = fyne.TextTruncateEllipsis
	g.modsLabel.Truncation = fyne.TextTruncateEllipsis

	g.list = widget.NewList(
		func() int { return len(g.mods) },
		func() fyne.CanvasObject { return newModRow(g) },
		func(id widget.ListItemID, o fyne.CanvasObject) { o.(*modRow).set(id) },
	)
	g.list.OnSelected = func(id widget.ListItemID) {
		if id < len(g.mods) {
			g.sel = g.mods[id]
			g.showDetails()
		}
	}
	g.details = container.NewVBox(widget.NewLabel("Select a mod to see its options."))

	install := widget.NewButtonWithIcon("Install mod…", theme.ContentAddIcon(), g.pickInstall)
	updates := widget.NewButtonWithIcon("Check for updates", theme.DownloadIcon(), func() { g.checkUpdates(true) })
	autoUpdates := widget.NewCheck("Check on start", nil)
	autoUpdates.Checked = g.cfg.CheckUpdates
	autoUpdates.OnChanged = func(on bool) {
		g.cfg.CheckUpdates = on
		g.saveConfig()
	}
	deploy := widget.NewButtonWithIcon("Deploy", theme.ConfirmIcon(), func() { g.deploy(false) })
	deploy.Importance = widget.HighImportance
	undeploy := widget.NewButtonWithIcon("Restore vanilla", theme.HistoryIcon(), g.undeploy)
	skipIntro := widget.NewCheck("Skip intro", nil)
	skipIntro.Checked = g.cfg.skipIntro() // before OnChanged, which saves
	skipIntro.OnChanged = func(on bool) {
		g.cfg.SkipIntro = &on
		g.saveConfig()
		g.refreshState()
	}
	g.actions = []fyne.Disableable{install, updates, deploy, undeploy}

	folders := container.New(layout.NewFormLayout(),
		widget.NewLabel("Game:"), container.NewBorder(nil, nil, nil, widget.NewButton("Change…", g.pickGame), g.gameLabel),
		widget.NewLabel("Mods:"), container.NewBorder(nil, nil, nil, widget.NewButton("Change…", g.pickMods), g.modsLabel),
	)
	top := container.NewVBox(folders, container.NewHBox(install, updates, autoUpdates, layout.NewSpacer(), skipIntro))

	g.logText = widget.NewLabel("Drop mod archives on this window to install them. Drag mods to change the load order.")
	g.logText.Wrapping = fyne.TextWrapWord
	g.logText.TextStyle = fyne.TextStyle{Monospace: true}
	g.logScroll = container.NewVScroll(g.logText)
	g.logScroll.SetMinSize(fyne.NewSize(0, 140))
	g.status = widget.NewLabel("")
	g.summary = widget.NewLabel("")
	g.summary.Wrapping = fyne.TextWrapWord
	g.working = widget.NewProgressBarInfinite()
	g.working.Hide()
	g.progress = widget.NewProgressBar()
	g.progress.Hide()
	bars := container.NewStack(g.working, g.progress)
	bottom := container.NewVBox(g.summary, container.NewHBox(deploy, undeploy, g.status), bars, g.logScroll)

	split := container.NewHSplit(g.list, container.NewVScroll(container.NewPadded(g.details)))
	split.Offset = 0.45
	g.win.SetContent(container.NewBorder(top, bottom, nil, nil, split))
	g.win.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		for _, u := range uris {
			g.install(u.Path())
		}
	})
	g.updateFolders()
}

// modRow is a line in the mod list: drag it up or down to change the load
// order.
type modRow struct {
	widget.BaseWidget
	g      *gui
	id     int
	check  *widget.Check
	name   *widget.Label
	state  *widget.Label // deployed, not deployed yet, …
	gear   *widget.Icon  // the mod has options
	update *widget.Icon  // a newer version is available
	bg     *canvas.Rectangle
	dragDY float32
}

func newModRow(g *gui) *modRow {
	r := &modRow{g: g, check: widget.NewCheck("", nil), name: widget.NewLabel(""), state: widget.NewLabel(""),
		gear: widget.NewIcon(theme.SettingsIcon()), update: widget.NewIcon(theme.DownloadIcon()), bg: canvas.NewRectangle(color.Transparent)}
	r.bg.CornerRadius = theme.InputRadiusSize()
	r.name.Truncation = fyne.TextTruncateEllipsis
	r.ExtendBaseWidget(r)
	return r
}

func (r *modRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(r.bg, container.NewBorder(nil, nil, r.check, container.NewHBox(r.update, r.gear, r.state), r.name)))
}

func (r *modRow) set(id int) {
	r.id = id
	m := r.g.mods[id]
	r.name.SetText(modLine(m))
	if hasOptions(nil, m) {
		r.gear.Show()
	} else {
		r.gear.Hide()
	}
	if _, ok := r.g.updates[m.Dir]; ok {
		r.update.Show()
	} else {
		r.update.Hide()
	}
	st := modDeployStatus(r.g.cfg, r.g.state, m)
	r.state.SetText(strings.TrimSuffix(st, ", not deployed yet"))
	switch st {
	case "deployed":
		r.state.Importance = widget.SuccessImportance
	case "":
	default:
		r.state.Importance = widget.WarningImportance
		if st != "not deployed yet" && st != "deployed, removed on next deploy" {
			r.state.SetText(r.state.Text + ", not deployed")
		}
	}
	r.state.Refresh()
	r.check.OnChanged = nil
	r.check.SetChecked(r.g.cfg.isEnabled(m.Dir))
	r.check.OnChanged = func(on bool) { r.g.setEnabled(m, on) }

	// while dragging: the target row lights up, the dragged one fades
	switch {
	case r.g.dragFrom >= 0 && id == r.g.dragTo:
		cr, cg, cb, _ := theme.Color(theme.ColorNamePrimary).RGBA()
		r.bg.FillColor = color.NRGBA{R: uint8(cr >> 8), G: uint8(cg >> 8), B: uint8(cb >> 8), A: 0x66}
	case r.g.dragFrom >= 0 && id == r.g.dragFrom:
		r.bg.FillColor = theme.Color(theme.ColorNameDisabledButton)
	default:
		r.bg.FillColor = color.Transparent
	}
	r.bg.Refresh()
}

func (r *modRow) Dragged(e *fyne.DragEvent) {
	r.dragDY += e.Dragged.DY
	if to, ok := r.g.dropTarget(r.id, r.dragDY, r.Size().Height); ok {
		r.g.status.SetText(fmt.Sprintf("Move %s to load order %d", r.g.mods[r.id].Title(), to+1))
		if r.g.dragFrom != r.id || r.g.dragTo != to {
			r.g.dragFrom, r.g.dragTo = r.id, to
			r.g.list.Refresh()
		}
	}
}

func (r *modRow) DragEnd() {
	dy := r.dragDY
	r.dragDY = 0
	r.g.status.SetText("")
	r.g.dragFrom, r.g.dragTo = -1, -1
	if to, ok := r.g.dropTarget(r.id, dy, r.Size().Height); ok && to != r.id {
		r.g.moveTo(r.g.mods[r.id], to)
	} else {
		r.g.list.Refresh()
	}
}

// dropTarget is the load order position a mod dragged by dy lands on. Only
// enabled mods have a place in the load order.
func (g *gui) dropTarget(id int, dy, rowHeight float32) (int, bool) {
	if id >= len(g.cfg.Enabled) || rowHeight <= 0 {
		return 0, false
	}
	pitch := rowHeight + theme.SeparatorThicknessSize()
	to := id + int(math.Round(float64(dy/pitch)))
	return max(0, min(to, len(g.cfg.Enabled)-1)), true
}

func modLine(m *Mod) string {
	s := m.Title()
	if m.Manifest != nil && m.Manifest.Version != "" {
		s += "  " + m.Manifest.Version
	}
	return s
}

func (g *gui) updateFolders() {
	g.gameLabel.SetText(g.cfg.abs(g.cfg.Game))
	g.modsLabel.SetText(g.cfg.abs(g.cfg.Mods))
}

func (g *gui) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	logLine("%s", line)
	fyne.Do(func() {
		g.logText.SetText(strings.TrimLeft(g.logText.Text+"\n"+line, "\n"))
		g.logScroll.ScrollToBottom()
	})
}

// showError shows the first line of an error, with the rest under Details
// and a button to copy it all; the whole error also goes to the log.
func (g *gui) showError(err error) {
	msg := err.Error()
	g.logf("error: %s", msg) // also to the log file
	summary, details, _ := strings.Cut(msg, "\n")
	content := container.NewVBox()
	s := widget.NewLabel(summary)
	s.Wrapping = fyne.TextWrapWord
	content.Add(s)
	if details != "" {
		d := widget.NewLabelWithStyle(strings.TrimSpace(details), fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
		d.Wrapping = fyne.TextWrapWord
		scroll := container.NewVScroll(d)
		scroll.SetMinSize(fyne.NewSize(640, 220))
		content.Add(widget.NewAccordion(widget.NewAccordionItem("Details", scroll)))
	}
	copyBtn := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		g.app.Clipboard().SetContent(msg)
	})
	note := widget.NewLabel("Also written to " + g.cfg.abs("h3mod.log"))
	note.Importance = widget.LowImportance
	content.Add(container.NewHBox(copyBtn, note))
	d := dialog.NewCustom("Something went wrong", "Close", content, g.win)
	d.Resize(fyne.NewSize(700, 0))
	d.Show()
}

func (g *gui) saveConfig() {
	if err := g.cfg.save(); err != nil {
		g.showError(err)
	}
}

// isGame reports whether dir is a HITMAN 3 installation.
func isGame(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "Runtime", "chunk0.rpkg"))
	return err == nil
}

// ensureGame finds the game folder, asking for it when it isn't where the
// config says or in a usual Steam location, then runs next.
func (g *gui) ensureGame(next func()) {
	if isGame(g.cfg.abs(g.cfg.Game)) {
		next()
		return
	}
	for _, dir := range steamGameDirs() {
		if isGame(dir) {
			g.cfg.Game = dir
			if !g.cfg.fresh {
				g.saveConfig()
			}
			g.updateFolders()
			next()
			return
		}
	}
	dialog.ShowInformation("Where is HITMAN 3?", "Choose the HITMAN 3 game folder (the one with Retail and Runtime in it).", g.win)
	g.pickGameThen(next)
}

// firstRun offers SMF's mods folder when there is no config yet, then runs
// next.
func (g *gui) firstRun(next func()) {
	if !g.cfg.fresh {
		next()
		return
	}
	dir := g.cfg.smfMods()
	if dir == "" {
		g.saveConfig()
		next()
		return
	}
	dialog.ShowConfirm("Use SMF's mods?",
		fmt.Sprintf("Found Simple Mod Framework's mods folder:\n%s\n\nUse it, with SMF's load order and options?\nOtherwise h3mod keeps its own mods folder.", dir),
		func(yes bool) {
			if yes {
				notes, err := g.cfg.useSMFMods(dir)
				for _, n := range notes {
					g.logf("%s", n)
				}
				if err != nil {
					g.showError(err)
				}
				g.updateFolders()
			} else {
				g.saveConfig()
			}
			next()
		}, g.win)
}

func steamGameDirs() []string {
	var roots []string
	if runtime.GOOS == "windows" {
		roots = []string{`C:\Program Files (x86)\Steam`, `C:\Program Files\Steam`, `D:\SteamLibrary`, `E:\SteamLibrary`}
	} else if home, err := os.UserHomeDir(); err == nil {
		roots = []string{filepath.Join(home, ".steam", "steam"), filepath.Join(home, ".local", "share", "Steam")}
	}
	var out []string
	for _, r := range roots {
		out = append(out, filepath.Join(r, "steamapps", "common", "HITMAN 3"))
	}
	return out
}

func (g *gui) pickGame() { g.pickGameThen(g.reload) }

func (g *gui) pickGameThen(next func()) {
	dialog.ShowFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil || u == nil {
			return
		}
		if !isGame(u.Path()) {
			g.showError(fmt.Errorf("%s is not a HITMAN 3 folder: it has no Runtime/chunk0.rpkg", u.Path()))
			return
		}
		g.cfg.Game = u.Path()
		if !g.cfg.fresh {
			g.saveConfig()
		}
		g.updateFolders()
		next()
	}, g.win)
}

func (g *gui) pickMods() {
	dialog.ShowFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil || u == nil {
			return
		}
		g.cfg.Mods = u.Path()
		g.saveConfig()
		g.updateFolders()
		g.reload()
	}, g.win)
}

// reload reads the mods folder again, enabling new mods like the CLI does.
func (g *gui) reload() {
	if err := os.MkdirAll(g.cfg.abs(g.cfg.Mods), 0o755); err != nil {
		g.showError(err)
		return
	}
	mods, notes, err := g.cfg.syncMods()
	if err != nil {
		g.showError(err)
		return
	}
	for _, n := range notes {
		g.logf("%s", n)
	}
	g.mods = listed(g.cfg, mods)
	if g.state, err = loadState(g.cfg.abs("state.json")); err != nil {
		g.showError(err)
		g.state = &State{}
	}
	g.refreshState()
	if g.sel != nil {
		g.sel = findMod(g.mods, g.sel.Dir)
	}
	g.list.Refresh()
	g.showDetails()
}

// refreshState updates what the list and summary say about deployment.
func (g *gui) refreshState() {
	line, pending := deployStatus(g.cfg, g.state, g.mods)
	if pending {
		line += ". Press Deploy to apply."
	}
	g.summary.SetText(strings.ToUpper(line[:1]) + line[1:])
	g.list.Refresh()
}

func (g *gui) setEnabled(m *Mod, on bool) {
	switch {
	case on && !g.cfg.isEnabled(m.Dir):
		g.cfg.Enabled = append(g.cfg.Enabled, m.Dir)
	case !on:
		g.cfg.Enabled = slices.DeleteFunc(g.cfg.Enabled, func(d string) bool { return d == m.Dir })
	}
	g.saveConfig()
	g.reload()
}

// moveTo puts an enabled mod at a load order position.
func (g *gui) moveTo(m *Mod, to int) {
	i := slices.Index(g.cfg.Enabled, m.Dir)
	if i < 0 || i == to || to < 0 || to >= len(g.cfg.Enabled) {
		return
	}
	g.cfg.Enabled = slices.Insert(slices.Delete(g.cfg.Enabled, i, i+1), to, m.Dir)
	g.saveConfig()
	g.reload()
	if k := slices.IndexFunc(g.mods, func(x *Mod) bool { return x.Dir == m.Dir }); k >= 0 {
		g.list.Select(k)
	}
}

func (g *gui) showDetails() {
	g.details.RemoveAll()
	m := g.sel
	if m == nil {
		g.details.Add(widget.NewLabel("Select a mod to see its options."))
		return
	}
	title := widget.NewLabelWithStyle(m.Title(), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	title.Wrapping = fyne.TextWrapWord
	g.details.Add(title)
	if man := m.Manifest; man != nil {
		info := []string{}
		if man.Version != "" {
			info = append(info, "version "+man.Version)
		}
		if len(man.Authors) > 0 {
			info = append(info, "by "+strings.Join(man.Authors, ", "))
		}
		if len(info) > 0 {
			g.details.Add(widget.NewLabel(strings.Join(info, ", ")))
		}
		if man.Description != nil && *man.Description != "" {
			d := widget.NewLabel(*man.Description)
			d.Wrapping = fyne.TextWrapWord
			g.details.Add(d)
		}
	} else {
		g.details.Add(widget.NewLabel("rpkg mod"))
	}

	if i := slices.Index(g.cfg.Enabled, m.Dir); i >= 0 {
		up := widget.NewButtonWithIcon("Load earlier", theme.MoveUpIcon(), func() { g.moveTo(m, i-1) })
		down := widget.NewButtonWithIcon("Load later", theme.MoveDownIcon(), func() { g.moveTo(m, i+1) })
		if i == 0 {
			up.Disable()
		}
		if i == len(g.cfg.Enabled)-1 {
			down.Disable()
		}
		g.details.Add(container.NewHBox(widget.NewLabel(fmt.Sprintf("Load order %d of %d (later wins)", i+1, len(g.cfg.Enabled))), up, down))
	}
	remove := widget.NewButtonWithIcon("Delete mod…", theme.DeleteIcon(), func() { g.remove(m) })
	remove.Importance = widget.DangerImportance
	buttons := container.NewHBox(remove)
	if f, ok := g.updates[m.Dir]; ok {
		update := widget.NewButtonWithIcon("Update to "+f.u.Version+"…", theme.DownloadIcon(), func() { g.offerUpdates([]foundUpdate{f}) })
		update.Importance = widget.HighImportance
		buttons.Add(update)
	}
	g.details.Add(buttons)

	if m.Manifest == nil || len(m.Manifest.Options) == 0 {
		return
	}
	g.details.Add(widget.NewSeparator())
	g.details.Add(widget.NewLabelWithStyle("Options", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	sel, _ := g.cfg.selection(m)
	setOption := func(key string, on bool) {
		if err := g.cfg.setOption(m, key, on); err != nil {
			g.showError(err)
			return
		}
		g.saveConfig()
		g.refreshState()
	}
	groups := map[string]bool{}
	for _, o := range m.Manifest.Options {
		switch o.Type {
		case "select":
			if groups[o.Group] {
				continue
			}
			groups[o.Group] = true
			var names []string
			selected := ""
			for _, c := range m.Manifest.Options {
				if c.Type == "select" && c.Group == o.Group {
					names = append(names, c.Name)
					if slices.Contains(sel, c.Key()) {
						selected = c.Name
					}
				}
			}
			r := widget.NewRadioGroup(names, nil)
			r.Required = true
			r.SetSelected(selected)
			group := o.Group
			r.OnChanged = func(name string) {
				if name != "" {
					setOption(group+":"+name, true)
				}
			}
			g.details.Add(widget.NewLabelWithStyle(o.Group, fyne.TextAlignLeading, fyne.TextStyle{Italic: true}))
			g.details.Add(r)
		case "conditional":
			g.details.Add(widget.NewLabel(o.Name + " (applied automatically)"))
		default:
			key := o.Key()
			c := widget.NewCheck(o.Name, func(on bool) { setOption(key, on) })
			c.Checked = slices.Contains(sel, key)
			g.details.Add(c)
		}
		if o.Tooltip != "" && o.Type != "select" {
			t := widget.NewLabel(o.Tooltip)
			t.Wrapping = fyne.TextWrapWord
			t.Importance = widget.LowImportance
			g.details.Add(t)
		}
	}
}

func (g *gui) remove(m *Mod) {
	dialog.ShowConfirm("Delete mod", fmt.Sprintf("Delete %s from the mods folder? Deploy afterwards to remove it from the game.", m.Title()), func(ok bool) {
		if !ok {
			return
		}
		if err := os.RemoveAll(m.Path); err != nil {
			g.showError(err)
		}
		g.sel = nil
		g.reload()
	}, g.win)
}

func (g *gui) pickInstall() {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			return
		}
		r.Close()
		g.install(r.URI().Path())
	}, g.win)
	d.SetFilter(storage.NewExtensionFileFilter([]string{".zip", ".7z", ".rar", ".rpkg"}))
	d.Resize(fyne.NewSize(800, 560))
	d.Show()
}

// busy runs work in the background with the action buttons disabled. Work
// that reports progress gets a bar that fills; other work an animated one.
func (g *gui) busy(withProgress bool, work func() error, done func(error)) {
	for _, a := range g.actions {
		a.Disable()
	}
	if withProgress {
		g.progress.SetValue(0)
		g.progress.Show()
	} else {
		g.working.Show()
		g.working.Start()
	}
	go func() {
		err := work()
		fyne.Do(func() {
			g.working.Stop()
			g.working.Hide()
			g.progress.Hide()
			g.status.SetText("")
			for _, a := range g.actions {
				a.Enable()
			}
			if done != nil {
				done(err)
			} else if err != nil {
				g.showError(err)
			}
		})
	}()
}

func (g *gui) install(path string) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	logLine("install %s", path)
	g.logf("installing %s", filepath.Base(path))
	g.busy(false, func() error {
		dirs, err := installMods(g.ctx, g.cfg, path, name, false, "", "")
		for _, d := range dirs {
			g.logf("installed %s", d)
		}
		return err
	}, func(err error) {
		if err != nil {
			g.showError(err)
		}
		g.reload()
	})
}

// checkUpdates looks for newer versions of the mods, marks them in the
// list and offers to install them. A check at start stays quiet when
// everything is up to date.
func (g *gui) checkUpdates(manual bool) {
	var found []foundUpdate
	work := func() error {
		found = findUpdates(g.ctx, g.mods, g.logf)
		return nil
	}
	done := func(error) {
		g.updates = map[string]foundUpdate{}
		for _, f := range found {
			g.updates[f.m.Dir] = f
		}
		g.list.Refresh()
		if len(found) == 0 {
			if manual {
				g.logf("all mods with an update URL are up to date")
			}
			return
		}
		g.offerUpdates(found)
	}
	if manual {
		g.logf("checking for updates")
		g.busy(false, work, done)
		return
	}
	go func() {
		work()
		fyne.Do(func() { done(nil) })
	}()
}

func (g *gui) offerUpdates(found []foundUpdate) {
	var b strings.Builder
	for _, f := range found {
		fmt.Fprintf(&b, "%s: %s → %s\n%s\n\n", f.m.Title(), f.m.Manifest.Version, f.u.Version, f.notes)
	}
	text := widget.NewLabel(strings.TrimSpace(b.String()))
	text.Wrapping = fyne.TextWrapWord
	scroll := container.NewVScroll(text)
	scroll.SetMinSize(fyne.NewSize(600, 360))
	title := fmt.Sprintf("%d mod updates", len(found))
	if len(found) == 1 {
		title = "1 mod update"
	}
	dialog.ShowCustomConfirm(title, "Install", "Later", scroll, func(ok bool) {
		if !ok {
			return
		}
		g.busy(false, func() error {
			for _, f := range found {
				if err := installUpdate(g.ctx, g.cfg, f.m, f.u); err != nil {
					return err
				}
				g.logf("%s updated to %s", f.m.Dir, f.u.Version)
			}
			return nil
		}, func(err error) {
			if err != nil {
				g.showError(err)
			}
			g.updates = nil
			g.reload()
		})
	}, g.win)
}

func (g *gui) deployer() *Deployer {
	d := newDeployer(g.cfg)
	d.log = g.logf
	d.progress = func(step string, done, total int) {
		fyne.Do(func() {
			g.status.SetText(fmt.Sprintf("%s %d/%d", step, done, total))
			if total > 0 {
				g.progress.SetValue(float64(done) / float64(total))
			}
		})
	}
	return d
}

func (g *gui) deploy(skipUnsupported bool) {
	logLine("deploy")
	g.logf("deploying")
	g.busy(true, func() error {
		return g.deployer().Deploy(g.ctx, skipUnsupported, "")
	}, func(err error) {
		switch {
		case errors.Is(err, errUnsupported):
			dialog.ShowConfirm("Unsupported features",
				"Some mods use features h3mod doesn't support (listed in the log). Deploy without them?",
				func(ok bool) {
					if ok {
						g.deploy(true)
					}
				}, g.win)
		case err != nil:
			g.showError(err)
		default:
			g.reload() // deploy may sort the load order, and changes what's deployed
		}
	})
}

func (g *gui) undeploy() {
	dialog.ShowConfirm("Restore vanilla", "Remove all deployed mods from the game? Your mods folder isn't touched.", func(ok bool) {
		if ok {
			logLine("undeploy")
			g.busy(false, g.deployer().Undeploy, func(err error) {
				if err != nil {
					g.showError(err)
				}
				g.reload()
			})
		}
	}, g.win)
}
