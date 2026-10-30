// Package tracker watches per-location mode transitions: it logs them to
// ttywx.log and fires the bell / desktop notification when a location
// first enters TAKE COVER.
package tracker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jdankney/ttywx/api"
	"github.com/jdankney/ttywx/modes"
)

type Tracker struct {
	Bell   bool
	Notify bool
	Demo   bool // tag log lines so simulated transitions aren't mistaken for real ones

	mu   sync.Mutex
	logf *os.File
	last map[string]modes.Mode
}

// New opens (appending) the log at path. An empty path disables logging.
func New(path string) (*Tracker, error) {
	t := &Tracker{last: map[string]modes.Mode{}}
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		t.logf = f
	}
	return t, nil
}

func (t *Tracker) Close() error {
	if t.logf != nil {
		return t.logf.Close()
	}
	return nil
}

// Observe records a location's current mode. Locations start out assumed
// NORMAL, so the first elevated reading is logged as a change too. It
// returns true when the location just entered TAKE COVER.
func (t *Tracker) Observe(location string, m modes.Mode, cause *api.Alert) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	old := t.last[location]
	if old == m {
		return false
	}
	t.last[location] = m
	event := "—"
	if cause != nil {
		event = cause.Event
	}
	t.logLine(fmt.Sprintf("%s  %-16s %s -> %s  (%s)", time.Now().Format(time.RFC3339), location, old, m, event))

	if m != modes.TakeCover {
		return false
	}
	if t.Bell {
		// Stderr shares the terminal but not Bubble Tea's output buffer, so
		// the bell can't land in the middle of a frame's escape sequences.
		os.Stderr.WriteString("\a")
	}
	if t.Notify && cause != nil {
		go notify("TAKE COVER: "+location, cause.Headline)
	}
	return true
}

func (t *Tracker) logLine(s string) {
	if t.logf == nil {
		return
	}
	if t.Demo {
		s += "  [demo]"
	}
	fmt.Fprintln(t.logf, s)
}

// notify sends a best-effort desktop notification; failures are ignored.
func notify(title, body string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd":
		if _, err := exec.LookPath("notify-send"); err != nil {
			return
		}
		cmd = exec.Command("notify-send", "-u", "critical", "-a", "ttywx", title, body)
	case "darwin":
		cmd = exec.Command("osascript", "-e",
			fmt.Sprintf("display notification %s with title %s sound name \"Sosumi\"", appleScriptString(body), appleScriptString(title)))
	default:
		return
	}
	_ = cmd.Run()
}

// appleScriptString quotes s as an AppleScript string literal, which only
// needs backslashes and double quotes escaped (unlike Go's %q).
func appleScriptString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
