//go:build gui

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
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

func main() {
	a := app.NewWithID("io.github.jim-ww.h3mod")
	g := &gui{app: a, win: a.NewWindow("h3mod " + version), ctx: context.Background()}
	cfg, err := loadConfig(guiConfigPath())
	g.cfg = cfg
	g.build()
	g.win.Resize(fyne.NewSize(1000, 680))
	if err != nil {
		g.win.Show()
		dialog.ShowError(err, g.win)
		a.Run()
		return
	}
	g.win.Show()
	g.ensureGame(g.reload)
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
	app  fyne.App
	win  fyne.Window
	ctx  context.Context
	cfg  *Config
	mods []*Mod // in status order: enabled in load order, then disabled
	sel  *Mod

	list      *widget.List
	details   *fyne.Container
	gameLabel *widget.Label
	modsLabel *widget.Label
	logText   *widget.Label
	logScroll *container.Scroll
	progress  *widget.ProgressBarInfinite
	actions   []fyne.Disableable
}

func (g *gui) build() {
	g.gameLabel = widget.NewLabel("")
	g.modsLabel = widget.NewLabel("")
	g.gameLabel.Truncation = fyne.TextTruncateEllipsis
	g.modsLabel.Truncation = fyne.TextTruncateEllipsis

	g.list = widget.NewList(
		func() int { return len(g.mods) },
		func() fyne.CanvasObject {
			name := widget.NewLabel("")
			name.Truncation = fyne.TextTruncateEllipsis
			return container.NewBorder(nil, nil, widget.NewCheck("", nil), nil, name)
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			m := g.mods[id]
			c := o.(*fyne.Container)
			name := c.Objects[0].(*widget.Label)
			check := c.Objects[1].(*widget.Check)
			name.SetText(modLine(m))
			check.OnChanged = nil
			check.SetChecked(g.cfg.isEnabled(m.Dir))
			check.OnChanged = func(on bool) { g.setEnabled(m, on) }
		},
	)
	g.list.OnSelected = func(id widget.ListItemID) {
		if id < len(g.mods) {
			g.sel = g.mods[id]
			g.showDetails()
		}
	}
	g.details = container.NewVBox(widget.NewLabel("Select a mod to see its options."))

	install := widget.NewButtonWithIcon("Install mod…", theme.ContentAddIcon(), g.pickInstall)
	updates := widget.NewButtonWithIcon("Check for updates", theme.DownloadIcon(), g.checkUpdates)
	deploy := widget.NewButtonWithIcon("Deploy", theme.ConfirmIcon(), func() { g.deploy(false) })
	deploy.Importance = widget.HighImportance
	undeploy := widget.NewButtonWithIcon("Restore vanilla", theme.HistoryIcon(), g.undeploy)
	skipIntro := widget.NewCheck("Skip intro", func(on bool) {
		g.cfg.SkipIntro = &on
		g.saveConfig()
	})
	skipIntro.SetChecked(g.cfg.skipIntro())
	g.actions = []fyne.Disableable{install, updates, deploy, undeploy}

	folders := container.New(layout.NewFormLayout(),
		widget.NewLabel("Game:"), container.NewBorder(nil, nil, nil, widget.NewButton("Change…", g.pickGame), g.gameLabel),
		widget.NewLabel("Mods:"), container.NewBorder(nil, nil, nil, widget.NewButton("Change…", g.pickMods), g.modsLabel),
	)
	top := container.NewVBox(folders, container.NewHBox(install, updates, layout.NewSpacer(), skipIntro))

	g.logText = widget.NewLabel("Drop mod archives on this window to install them.")
	g.logText.Wrapping = fyne.TextWrapWord
	g.logText.TextStyle = fyne.TextStyle{Monospace: true}
	g.logScroll = container.NewVScroll(g.logText)
	g.logScroll.SetMinSize(fyne.NewSize(0, 140))
	g.progress = widget.NewProgressBarInfinite()
	g.progress.Hide()
	bottom := container.NewVBox(container.NewHBox(deploy, undeploy), g.progress, g.logScroll)

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
	fyne.Do(func() {
		g.logText.SetText(strings.TrimLeft(g.logText.Text+"\n"+line, "\n"))
		g.logScroll.ScrollToBottom()
	})
}

func (g *gui) saveConfig() {
	if err := g.cfg.save(); err != nil {
		dialog.ShowError(err, g.win)
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
			g.saveConfig()
			g.updateFolders()
			next()
			return
		}
	}
	dialog.ShowInformation("Where is HITMAN 3?", "Choose the HITMAN 3 game folder (the one with Retail and Runtime in it).", g.win)
	g.pickGame()
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

func (g *gui) pickGame() {
	dialog.ShowFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil || u == nil {
			return
		}
		if !isGame(u.Path()) {
			dialog.ShowError(fmt.Errorf("%s is not a HITMAN 3 folder: it has no Runtime/chunk0.rpkg", u.Path()), g.win)
			return
		}
		g.cfg.Game = u.Path()
		g.saveConfig()
		g.updateFolders()
		g.reload()
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
		dialog.ShowError(err, g.win)
		return
	}
	mods, notes, err := g.cfg.syncMods()
	if err != nil {
		dialog.ShowError(err, g.win)
		return
	}
	for _, n := range notes {
		g.logf("%s", n)
	}
	g.mods = listed(g.cfg, mods)
	if g.sel != nil {
		g.sel = findMod(g.mods, g.sel.Dir)
	}
	g.list.Refresh()
	g.showDetails()
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

// move shifts an enabled mod by delta places in the load order.
func (g *gui) move(m *Mod, delta int) {
	i := slices.Index(g.cfg.Enabled, m.Dir)
	j := i + delta
	if i < 0 || j < 0 || j >= len(g.cfg.Enabled) {
		return
	}
	g.cfg.Enabled[i], g.cfg.Enabled[j] = g.cfg.Enabled[j], g.cfg.Enabled[i]
	g.saveConfig()
	g.reload()
	if k := slices.Index(g.mods, m); k >= 0 {
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
		up := widget.NewButtonWithIcon("Load earlier", theme.MoveUpIcon(), func() { g.move(m, -1) })
		down := widget.NewButtonWithIcon("Load later", theme.MoveDownIcon(), func() { g.move(m, 1) })
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
	g.details.Add(container.NewHBox(remove))

	if m.Manifest == nil || len(m.Manifest.Options) == 0 {
		return
	}
	g.details.Add(widget.NewSeparator())
	g.details.Add(widget.NewLabelWithStyle("Options", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	sel, _ := g.cfg.selection(m)
	setOption := func(key string, on bool) {
		if err := g.cfg.setOption(m, key, on); err != nil {
			dialog.ShowError(err, g.win)
			return
		}
		g.saveConfig()
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
			dialog.ShowError(err, g.win)
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

// busy runs work in the background with the action buttons disabled.
func (g *gui) busy(work func() error, done func(error)) {
	for _, a := range g.actions {
		a.Disable()
	}
	g.progress.Show()
	g.progress.Start()
	go func() {
		err := work()
		fyne.Do(func() {
			g.progress.Stop()
			g.progress.Hide()
			for _, a := range g.actions {
				a.Enable()
			}
			if done != nil {
				done(err)
			} else if err != nil {
				dialog.ShowError(err, g.win)
			}
		})
	}()
}

func (g *gui) install(path string) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	g.logf("installing %s", filepath.Base(path))
	g.busy(func() error {
		dirs, err := installMods(g.ctx, g.cfg, path, name, false, "", "")
		for _, d := range dirs {
			g.logf("installed %s", d)
		}
		return err
	}, func(err error) {
		if err != nil {
			dialog.ShowError(err, g.win)
		}
		g.reload()
	})
}

func (g *gui) checkUpdates() {
	type found struct {
		m     *Mod
		u     modUpdate
		notes string
	}
	var updates []found
	g.logf("checking for updates")
	g.busy(func() error {
		for _, m := range g.mods {
			if m.Manifest == nil || m.Manifest.UpdateCheck == "" {
				continue
			}
			u, newer, err := checkUpdate(g.ctx, m)
			switch {
			case err != nil:
				g.logf("%s: %v", m.Dir, err)
			case newer:
				updates = append(updates, found{m, u, changelog(g.ctx, m, u)})
			}
		}
		return nil
	}, func(error) {
		if len(updates) == 0 {
			g.logf("all mods with an update URL are up to date")
			return
		}
		var b strings.Builder
		for _, f := range updates {
			fmt.Fprintf(&b, "%s: %s → %s\n%s\n\n", f.m.Title(), f.m.Manifest.Version, f.u.Version, f.notes)
		}
		text := widget.NewLabel(strings.TrimSpace(b.String()))
		text.Wrapping = fyne.TextWrapWord
		scroll := container.NewVScroll(text)
		scroll.SetMinSize(fyne.NewSize(600, 360))
		dialog.ShowCustomConfirm(fmt.Sprintf("%d updates", len(updates)), "Install", "Later", scroll, func(ok bool) {
			if !ok {
				return
			}
			g.busy(func() error {
				for _, f := range updates {
					if err := installUpdate(g.ctx, g.cfg, f.m, f.u); err != nil {
						return err
					}
					g.logf("%s updated to %s", f.m.Dir, f.u.Version)
				}
				return nil
			}, func(err error) {
				if err != nil {
					dialog.ShowError(err, g.win)
				}
				g.reload()
			})
		}, g.win)
	})
}

func (g *gui) deployer() *Deployer {
	d := newDeployer(g.cfg)
	d.log = g.logf
	return d
}

func (g *gui) deploy(skipUnsupported bool) {
	g.logf("deploying")
	g.busy(func() error {
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
			dialog.ShowError(err, g.win)
		default:
			g.reload() // deploy may sort the load order
		}
	})
}

func (g *gui) undeploy() {
	dialog.ShowConfirm("Restore vanilla", "Remove all deployed mods from the game? Your mods folder isn't touched.", func(ok bool) {
		if ok {
			g.busy(g.deployer().Undeploy, nil)
		}
	}, g.win)
}
