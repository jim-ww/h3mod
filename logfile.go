package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Runs are logged to h3mod.log next to the config, so a failed deploy can be
// looked at (or sent along) afterwards. At 1 MB the log moves to
// h3mod.log.1.

const logLimit = 1 << 20

var runLog struct {
	sync.Mutex
	f *os.File
}

// openRunLog starts logging to the config's h3mod.log with a header line.
func openRunLog(cfg *Config, header string) {
	path := cfg.abs("h3mod.log")
	if st, err := os.Stat(path); err == nil && st.Size() > logLimit {
		os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return // logging is best effort
	}
	runLog.Lock()
	if runLog.f != nil {
		runLog.f.Close()
	}
	runLog.f = f
	runLog.Unlock()
	logLine("--- %s", header)
}

// logLine appends a timestamped line to the run log, if one is open.
func logLine(format string, args ...any) {
	runLog.Lock()
	defer runLog.Unlock()
	if runLog.f != nil {
		fmt.Fprintf(runLog.f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	}
}
