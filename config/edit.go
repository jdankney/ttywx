package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Location edits touch only the lines that change, so the rest of a
// hand-edited file (blank lines, comment alignment) survives. If the file's
// layout is unusual and the line edit doesn't produce exactly the intended
// locations, they fall back to re-encoding the YAML document, which keeps
// comments but not spacing.

// AddLocation appends loc to the config at path. If loc.Primary is set, it
// replaces any existing primary.
func AddLocation(path string, loc Location) error {
	return editFile(path, func(data []byte, old *Config) ([]byte, []Location, error) {
		for _, l := range old.Locations {
			if strings.EqualFold(l.Name, loc.Name) {
				return nil, nil, fmt.Errorf("a location named %q already exists (pick another with --name)", loc.Name)
			}
		}
		want := append(slices.Clone(old.Locations), loc)
		if loc.Primary {
			want = append([]Location{loc}, old.Locations...)
		}
		return addLines(data, loc), want, nil
	}, func(seq *yaml.Node) {
		if loc.Primary {
			for _, item := range seq.Content {
				removeField(item, "primary")
			}
		}
		var n yaml.Node
		if yaml.Unmarshal([]byte(flowLocation(loc)), &n) == nil {
			seq.Content = append(seq.Content, n.Content[0])
		}
	})
}

// RemoveLocation deletes the location with the given name (case-insensitive)
// from the config at path. It refuses to remove the last one.
func RemoveLocation(path, name string) error {
	return editFile(path, func(data []byte, old *Config) ([]byte, []Location, error) {
		i := slices.IndexFunc(old.Locations, func(l Location) bool { return strings.EqualFold(l.Name, name) })
		if i < 0 {
			return nil, nil, fmt.Errorf("no location named %q", name)
		}
		if len(old.Locations) == 1 {
			return nil, nil, errors.New("can't remove the only location; add another first")
		}
		return removeLines(data, name), slices.Delete(slices.Clone(old.Locations), i, i+1), nil
	}, func(seq *yaml.Node) {
		seq.Content = slices.DeleteFunc(seq.Content, func(n *yaml.Node) bool {
			return strings.EqualFold(field(n, "name"), name)
		})
	})
}

// editFile loads path and asks lineEdit for the edited text and the
// locations it should contain. If the text parses to exactly those it is
// written; otherwise nodeEdit is applied to the parsed document instead.
func editFile(path string,
	lineEdit func(data []byte, old *Config) ([]byte, []Location, error),
	nodeEdit func(seq *yaml.Node),
) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	old, err := Parse(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	out, want, err := lineEdit(data, old)
	if err != nil {
		return err
	}
	if out == nil || !sameLocations(out, want) {
		if out, err = reencode(data, nodeEdit); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !sameLocations(out, want) {
			return fmt.Errorf("%s: couldn't edit the locations list; please edit it by hand", path)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sameLocations reports whether data parses to the want locations, with
// the primary rule applied (first one marked primary, else the first).
func sameLocations(data []byte, want []Location) bool {
	got, err := Parse(data)
	if err != nil {
		return false
	}
	key := func(l Location) string {
		return fmt.Sprintf("%s|%.4f|%.4f", strings.ToLower(l.Name), l.Lat, l.Lon)
	}
	keys := func(ls []Location) []string {
		var out []string
		for _, l := range ls {
			out = append(out, key(l))
		}
		slices.Sort(out)
		return out
	}
	primary := max(0, slices.IndexFunc(want, func(l Location) bool { return l.Primary }))
	return key(got.Locations[0]) == key(want[primary]) && slices.Equal(keys(got.Locations), keys(want))
}

// ── line edits ────────────────────────────────────────────────────────────

// locationItems returns the items of a block-style locations list, or nil.
func locationItems(data []byte) []*yaml.Node {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) == 0 {
		return nil
	}
	seq := mapValue(doc.Content[0], "locations")
	if seq == nil || seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 {
		return nil
	}
	return seq.Content
}

var primaryRe = []*regexp.Regexp{
	regexp.MustCompile(`,\s*primary:\s*\w+`),    // { name: X, primary: true }
	regexp.MustCompile(`primary:\s*\w+\s*,\s*`), // { primary: true, name: X }
}

// addLines inserts loc as a flow-style item after the last one, stripping
// other items' primary flags if loc is primary. Returns nil if it can't.
func addLines(data []byte, loc Location) []byte {
	items := locationItems(data)
	if len(items) == 0 {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	first := lines[items[0].Line-1]
	dash := strings.Index(first, "-")
	if dash < 0 || strings.TrimSpace(first[:dash]) != "" {
		return nil
	}
	var drop []int
	if loc.Primary {
		for _, item := range items {
			k := keyNode(item, "primary")
			if k == nil {
				continue
			}
			i := k.Line - 1
			if item.Style&yaml.FlowStyle == 0 {
				drop = append(drop, i) // block mapping: the key has its own line
				continue
			}
			for _, re := range primaryRe {
				if s := re.ReplaceAllLiteralString(lines[i], ""); s != lines[i] {
					lines[i] = s
					break
				}
			}
		}
	}
	// maxLine is the 1-based last line of the item, i.e. the index after it.
	lines = slices.Insert(lines, maxLine(items[len(items)-1]), first[:dash]+"- "+flowLocation(loc))
	for _, i := range slices.Backward(drop) {
		lines = slices.Delete(lines, i, i+1)
	}
	return []byte(strings.Join(lines, "\n"))
}

// removeLines deletes the lines of the named item. Returns nil if it can't.
func removeLines(data []byte, name string) []byte {
	items := locationItems(data)
	lines := strings.Split(string(data), "\n")
	for i, item := range items {
		if !strings.EqualFold(field(item, "name"), name) {
			continue
		}
		from, to := item.Line-1, maxLine(item)
		if i+1 < len(items) {
			to = items[i+1].Line - 1
		}
		// Keep comments and blank lines between this item and the next;
		// they more likely belong to what follows.
		for to > from+1 && isFiller(lines[to-1]) {
			to--
		}
		// A comment directly above the item describes it, so it goes too
		// (unless the item is first, where it may describe the whole list).
		for i > 0 && from > 0 && strings.HasPrefix(strings.TrimSpace(lines[from-1]), "#") {
			from--
		}
		return []byte(strings.Join(slices.Delete(lines, from, to), "\n"))
	}
	return nil
}

func isFiller(s string) bool {
	t := strings.TrimSpace(s)
	return t == "" || strings.HasPrefix(t, "#")
}

func maxLine(n *yaml.Node) int {
	m := n.Line
	for _, c := range n.Content {
		m = max(m, maxLine(c))
	}
	return m
}

// ── node edits ────────────────────────────────────────────────────────────

func reencode(data []byte, edit func(seq *yaml.Node)) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not a YAML mapping")
	}
	seq := mapValue(doc.Content[0], "locations")
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil, errors.New("no locations list")
	}
	edit(seq)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func keyNode(m *yaml.Node, key string) *yaml.Node {
	if m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i]
		}
	}
	return nil
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if k := keyNode(m, key); k != nil {
		i := slices.Index(m.Content, k)
		return m.Content[i+1]
	}
	return nil
}

func field(m *yaml.Node, key string) string {
	if v := mapValue(m, key); v != nil {
		return v.Value
	}
	return ""
}

func removeField(m *yaml.Node, key string) {
	if k := keyNode(m, key); k != nil {
		i := slices.Index(m.Content, k)
		m.Content = slices.Delete(m.Content, i, i+2)
	}
}
