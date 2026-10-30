// ttywx is a terminal dashboard for National Weather Service conditions,
// forecasts and alerts at the places you choose.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jdankney/ttywx/api"
	"github.com/jdankney/ttywx/config"
	"github.com/jdankney/ttywx/tracker"
	"github.com/jdankney/ttywx/ui"
)

const usage = `ttywx: a terminal dashboard for National Weather Service data

Usage:
  ttywx [flags]                    open the dashboard
  ttywx add [--primary] [--name NAME] PLACE
                                   add a location; PLACE is "City, ST",
                                   a ZIP code, or "lat,lon"
  ttywx remove NAME                remove a location

With no config file, ttywx asks for your location and creates one.

Flags:
`

// globalFlags are accepted before or after a subcommand.
type globalFlags struct {
	config, contact *string
}

func addGlobalFlags(fs *flag.FlagSet) globalFlags {
	return globalFlags{
		config:  fs.String("config", "", "path to the config file (default ~/.config/ttywx/config.yaml)"),
		contact: fs.String("contact", "", "contact for the API User-Agent (overrides config and TTYWX_CONTACT)"),
	}
}

func main() {
	g := addGlobalFlags(flag.CommandLine)
	demoMode := flag.Bool("demo", false, "inject fake alerts to preview every threat mode (m / M cycle modes)")
	once := flag.Bool("once", false, "print a plain-text summary and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usage)
		flag.PrintDefaults()
	}
	flag.Parse()
	setVersion()

	if *showVersion {
		fmt.Println("ttywx", api.Version)
		return
	}

	switch cmd := flag.Arg(0); cmd {
	case "":
	case "add":
		os.Exit(cmdAdd(g, flag.Args()[1:]))
	case "remove", "rm":
		os.Exit(cmdRemove(g, flag.Args()[1:]))
	case "help":
		flag.Usage()
		return
	default:
		fail(2, "unknown command %q (see ttywx --help)", cmd)
	}

	path := configPath(g)
	cfg, err := config.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		if !interactive() {
			fail(2, "no config at %s\nRun ttywx in a terminal to set one up, or: ttywx add \"City, ST\"", path)
		}
		if err = setup(path, contactOverride(g)); err == nil {
			cfg, err = config.Load(path)
		}
	}
	if err != nil {
		fail(2, "%v", err)
	}
	if c := contactOverride(g); c != "" {
		cfg.Contact = c
	}
	client := api.NewClient(cfg.Contact)
	client.SI = cfg.Display().Metric

	if *once {
		os.Exit(runOnce(cfg, client, *demoMode, os.Stdout))
	}

	tr, err := tracker.New(cfg.LogPath())
	if err != nil {
		fail(2, "opening log: %v", err)
	}
	defer tr.Close()
	tr.Bell, tr.Notify, tr.Demo = cfg.Bell, cfg.Notify, *demoMode

	m := ui.New(cfg, client, tr, *demoMode)
	defer m.Shutdown()
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ttywx:", err)
		os.Exit(1)
	}
}

// setVersion fills in api.Version from the module version when built with
// `go install ...@vX` rather than by a release build that sets it directly.
func setVersion() {
	if api.Version != "dev" {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		api.Version = bi.Main.Version
	}
}

func configPath(g globalFlags) string {
	if *g.config != "" {
		return *g.config
	}
	p, err := config.DefaultPath()
	if err != nil {
		fail(2, "finding config dir: %v (use --config)", err)
	}
	return p
}

// contactOverride is --contact, else TTYWX_CONTACT, else "".
func contactOverride(g globalFlags) string {
	if *g.contact != "" {
		return *g.contact
	}
	return os.Getenv("TTYWX_CONTACT")
}

func fail(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ttywx: "+format+"\n", args...)
	os.Exit(code)
}
