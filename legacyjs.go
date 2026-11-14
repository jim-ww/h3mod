package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
)

// jsModules runs SMF's JavaScript QuickEntity versions with goja: a minimal
// CommonJS loader over a folder with the scripts and their node_modules, and
// the few Node APIs they use (fs, path, process.cwd). Their GUI paths
// (electron, dialogs) only load when a script is the main module, which it
// never is here.
type jsModules struct {
	vm    *goja.Runtime
	dir   string
	cache map[string]*goja.Object // module.exports by file
}

func newJSModules(dir, cwd string) *jsModules {
	m := &jsModules{vm: goja.New(), dir: dir, cache: map[string]*goja.Object{}}
	vm := m.vm
	console := vm.NewObject()
	for _, f := range []string{"log", "warn", "error", "info", "debug"} {
		console.Set(f, func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	}
	vm.Set("console", console)
	process := vm.NewObject()
	process.Set("cwd", func() string { return cwd })
	process.Set("argv", []string{})
	process.Set("env", map[string]string{})
	process.Set("platform", "linux")
	vm.Set("process", process)
	return m
}

func (m *jsModules) throw(err error) {
	panic(m.vm.NewGoError(err))
}

// native are the Node built-in modules the scripts require.
func (m *jsModules) native(name string) (*goja.Object, bool) {
	vm := m.vm
	o := vm.NewObject()
	switch name {
	case "fs":
		o.Set("readFileSync", func(p string) string {
			b, err := os.ReadFile(p)
			if err != nil {
				m.throw(err)
			}
			return string(b)
		})
		o.Set("writeFileSync", func(p, data string) {
			if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
				m.throw(err)
			}
		})
		o.Set("existsSync", func(p string) bool { _, err := os.Stat(p); return err == nil })
		o.Set("unlinkSync", func(p string) {
			if err := os.Remove(p); err != nil {
				m.throw(err)
			}
		})
	case "path":
		o.Set("join", func(parts ...string) string { return filepath.Join(parts...) })
		o.Set("basename", func(p string, ext ...string) string {
			b := filepath.Base(p)
			if len(ext) > 0 {
				b = strings.TrimSuffix(b, ext[0])
			}
			return b
		})
		o.Set("dirname", filepath.Dir)
		o.Set("extname", filepath.Ext)
		o.Set("sep", string(filepath.Separator))
	case "util":
		o.Set("promisify", func(goja.FunctionCall) goja.Value {
			m.throw(errors.New("util.promisify is not available"))
			return nil
		})
	case "child_process", "electron", "sweetalert2", "electron-json-storage":
		// only used by the scripts' GUI and packaging functions
		o.Set("execSync", func(goja.FunctionCall) goja.Value {
			m.throw(fmt.Errorf("%s is not available", name))
			return nil
		})
	default:
		return nil, false
	}
	return o, true
}

// resolve finds the file a require of name from dir means.
func (m *jsModules) resolve(name, dir string) (string, error) {
	var base string
	if strings.HasPrefix(name, "./") || strings.HasPrefix(name, "../") {
		base = filepath.Join(dir, name)
	} else {
		pkg := filepath.Join(m.dir, "node_modules", name)
		main := "index.js"
		if b, err := os.ReadFile(filepath.Join(pkg, "package.json")); err == nil {
			var p struct {
				Main string `json:"main"`
			}
			if json.Unmarshal(b, &p) == nil && p.Main != "" {
				main = p.Main
			}
		}
		base = filepath.Join(pkg, main)
	}
	for _, p := range []string{base, base + ".js", filepath.Join(base, "index.js")} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("cannot find module %q", name)
}

func (m *jsModules) require(name, dir string) *goja.Object {
	if o, ok := m.native(name); ok {
		return o
	}
	file, err := m.resolve(name, dir)
	if err != nil {
		m.throw(err)
	}
	if exp, ok := m.cache[file]; ok {
		return exp
	}
	src, err := os.ReadFile(file)
	if err != nil {
		m.throw(err)
	}
	prg, err := goja.Compile(file, "(function (exports, require, module, __filename, __dirname) {"+string(src)+"\n})", false)
	if err != nil {
		m.throw(err)
	}
	fn, err := m.vm.RunProgram(prg)
	if err != nil {
		m.throw(err)
	}
	call, _ := goja.AssertFunction(fn)
	module := m.vm.NewObject()
	exports := m.vm.NewObject()
	module.Set("exports", exports)
	module.Set("parent", m.vm.NewObject()) // never the main module
	module.Set("filename", file)
	m.cache[file] = exports // for require cycles
	req := func(n string) *goja.Object { return m.require(n, filepath.Dir(file)) }
	if _, err := call(goja.Undefined(), exports, m.vm.ToValue(req), module, m.vm.ToValue(file), m.vm.ToValue(filepath.Dir(file))); err != nil {
		m.throw(err)
	}
	exp := module.Get("exports").ToObject(m.vm)
	m.cache[file] = exp
	return exp
}

// globals sets up what the scripts expect from SMF's process: QuickEntity
// 1.136 uses three.js's math as a global THREE, with its old Math name.
func (m *jsModules) globals() {
	three := m.require("./three-onlymath.min.js", m.dir)
	if three.Get("Math") == nil {
		three.Set("Math", three.Get("MathUtils"))
	}
	m.vm.Set("THREE", three)
}

// call runs an exported async function of a script in m.dir and waits for
// its promise.
func (m *jsModules) call(script, fn string, args ...any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if v, ok := r.(*goja.Object); ok {
				err = errors.New(v.String())
				return
			}
			panic(r)
		}
	}()
	m.globals()
	exp := m.require("./"+script, m.dir)
	f, ok := goja.AssertFunction(exp.Get(fn))
	if !ok {
		return fmt.Errorf("%s has no function %s", script, fn)
	}
	vals := make([]goja.Value, len(args))
	for i, a := range args {
		vals[i] = m.vm.ToValue(a)
	}
	ret, err := f(goja.Undefined(), vals...)
	if err != nil {
		return err
	}
	// goja runs the promise jobs before the call returns; the scripts only
	// await synchronous work.
	if p, ok := ret.Export().(*goja.Promise); ok {
		switch p.State() {
		case goja.PromiseStateRejected:
			return fmt.Errorf("%s.%s: %v", script, fn, p.Result())
		case goja.PromiseStatePending:
			return fmt.Errorf("%s.%s did not finish", script, fn)
		}
	}
	return nil
}
