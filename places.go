package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/j4mes/ttywx/api"
	"github.com/j4mes/ttywx/config"
	"github.com/j4mes/ttywx/geo"
)

// This file holds the commands that manage locations: `ttywx add`,
// `ttywx remove`, and the first-run setup.

func interactive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

var stdin = bufio.NewReader(os.Stdin)

// prompt prints question and reads one trimmed line. EOF (ctrl+d) is an
// error so setup can be cancelled.
func prompt(question string) (string, error) {
	fmt.Print(question)
	line, err := stdin.ReadString('\n')
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		fmt.Println()
		return "", errors.New("cancelled")
	}
	return strings.TrimSpace(line), nil
}

func cmdAdd(g globalFlags, args []string) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	g2 := addGlobalFlags(fs)
	primary := fs.Bool("primary", false, "make this the primary location (shown on the Main tab)")
	name := fs.String("name", "", "display name (default: the place's name)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: ttywx add [--primary] [--name NAME] PLACE\n\nPLACE is \"City, ST\", a ZIP code, or \"lat,lon\".\n\nFlags:")
		fs.PrintDefaults()
	}
	words, err := parseInterleaved(fs, args)
	if err != nil {
		return 2
	}
	g = mergeFlags(g, g2)
	query := strings.Join(words, " ")
	if query == "" {
		fs.Usage()
		return 2
	}

	path := configPath(g)
	contact := contactOverride(g)
	cfg, err := config.Load(path)
	exists := !errors.Is(err, os.ErrNotExist)
	switch {
	case err != nil && exists:
		fmt.Fprintln(os.Stderr, "ttywx:", err)
		return 1
	case exists && contact == "":
		contact = cfg.Contact
	}

	loc, err := findLocation(contact, query, interactive())
	if err != nil {
		fmt.Fprintln(os.Stderr, "ttywx:", err)
		return 1
	}
	if *name != "" {
		loc.Name = *name
	}
	loc.Primary = *primary || !exists

	if exists {
		err = config.AddLocation(path, loc)
	} else {
		err = config.Write(path, config.Template(contact, []config.Location{loc}))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ttywx:", err)
		return 1
	}
	verb := "Added"
	if !exists {
		verb = "Created " + path + " with"
	}
	fmt.Printf("%s %s (%.4f, %.4f)", verb, loc.Name, loc.Lat, loc.Lon)
	if loc.Primary && exists {
		fmt.Print(" as the primary location")
	}
	if exists {
		fmt.Printf(" to %s", path)
	}
	fmt.Println(".")
	return 0
}

func cmdRemove(g globalFlags, args []string) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	g2 := addGlobalFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: ttywx remove NAME\n\nFlags:")
		fs.PrintDefaults()
	}
	words, err := parseInterleaved(fs, args)
	if err != nil {
		return 2
	}
	g = mergeFlags(g, g2)
	name := strings.Join(words, " ")
	if name == "" {
		fs.Usage()
		return 2
	}
	path := configPath(g)
	if err := config.RemoveLocation(path, name); err != nil {
		fmt.Fprintln(os.Stderr, "ttywx:", err)
		return 1
	}
	fmt.Printf("Removed %s from %s.\n", name, path)
	return 0
}

// parseInterleaved parses flags that may appear before, between or after
// positional words, returning the words.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var words []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return words, nil
		}
		words = append(words, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// mergeFlags prefers global flags given after the subcommand.
func mergeFlags(before, after globalFlags) globalFlags {
	if *after.config != "" {
		before.config = after.config
	}
	if *after.contact != "" {
		before.contact = after.contact
	}
	return before
}

// setup creates a config at path by asking for the first location.
func setup(path, contact string) error {
	fmt.Printf("Welcome to ttywx! There's no config at %s yet, so let's make one.\n", path)
	fmt.Println("ttywx shows National Weather Service data, which covers the US and its territories.")
	fmt.Println()
	var loc config.Location
	for {
		q, err := prompt("Your main location (\"City, ST\", ZIP code, or lat,lon): ")
		if err != nil {
			return err
		}
		if q == "" {
			continue
		}
		if loc, err = findLocation(contact, q, true); err == nil {
			break
		}
		fmt.Println("  " + err.Error())
	}
	loc.Primary = true

	if contact == "" {
		fmt.Println()
		fmt.Println("NWS asks apps to identify someone it can reach if their traffic misbehaves.")
		c, err := prompt("Your email or website (optional, press enter to skip): ")
		if err != nil {
			return err
		}
		contact = c
	}
	if err := config.Write(path, config.Template(contact, []config.Location{loc})); err != nil {
		return err
	}
	fmt.Printf("\nSaved %s.\nAdd more places any time with: ttywx add \"City, ST\"\n\n", path)
	time.Sleep(time.Second) // let the message register before the dashboard takes over
	return nil
}

// findLocation resolves query to a location, asking the user to choose
// between matches when ask is set, and checks that NWS covers it.
func findLocation(contact, query string, ask bool) (config.Location, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if contact == "" {
		contact = config.PlaceholderContact
	}
	ua := fmt.Sprintf("ttywx/%s (contact: %s)", api.Version, contact)

	var loc config.Location
	if lat, lon, ok := geo.ParseCoords(query); ok {
		loc = config.Location{Lat: lat, Lon: lon}
	} else {
		places, err := geo.NewClient(ua).Search(ctx, query)
		if err != nil {
			return loc, err
		}
		if len(places) == 0 {
			return loc, fmt.Errorf("no US places match %q (try \"City, ST\" or a ZIP code)", query)
		}
		p, err := choosePlace(places, ask)
		if err != nil {
			return loc, err
		}
		loc = config.Location{Name: p.Name, Lat: p.Lat, Lon: p.Lon}
	}
	loc.Lat, loc.Lon = round4(loc.Lat), round4(loc.Lon)

	pt, err := api.NewClient(contact).ResolvePoint(ctx, loc.Lat, loc.Lon)
	switch {
	case errors.Is(err, api.ErrNoCoverage):
		return loc, fmt.Errorf("%.4f, %.4f is %v", loc.Lat, loc.Lon, err)
	case err != nil:
		fmt.Fprintf(os.Stderr, "ttywx: couldn't check the location with NWS (%v); adding it anyway\n", err)
	case loc.Name == "":
		loc.Name = pt.City
	}
	if loc.Name == "" {
		loc.Name = fmt.Sprintf("%.2f,%.2f", loc.Lat, loc.Lon)
	}
	return loc, nil
}

func choosePlace(places []geo.Place, ask bool) (geo.Place, error) {
	if len(places) == 1 {
		return places[0], nil
	}
	if !ask {
		fmt.Fprintf(os.Stderr, "ttywx: using %s; %d other places match (add \", ST\" to be specific)\n", places[0].Label(), len(places)-1)
		return places[0], nil
	}
	fmt.Println("Which one?")
	for i, p := range places {
		fmt.Printf("  %2d) %s\n", i+1, p.Label())
	}
	for {
		s, err := prompt(fmt.Sprintf("Choose 1-%d [1]: ", len(places)))
		if err != nil {
			return geo.Place{}, err
		}
		if s == "" {
			return places[0], nil
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(places) {
			return places[n-1], nil
		}
	}
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }
