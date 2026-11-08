// Package diff compares two order snapshots.
package diff

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Kind string

const (
	Added   Kind = "added"
	Removed Kind = "removed"
	Changed Kind = "changed"
)

type Change struct {
	Kind Kind
	Path string
	Old  any
	New  any
}

func (c Change) String() string {
	switch c.Kind {
	case Added:
		return fmt.Sprintf("+ %s: %s", c.Path, Format(c.New))
	case Removed:
		return fmt.Sprintf("- %s (was %s)", c.Path, Format(c.Old))
	default:
		return fmt.Sprintf("~ %s: %s -> %s", c.Path, Format(c.Old), Format(c.New))
	}
}

type missing struct{}

// Diff returns the changes from old to new, sorted by path.
//
// Paths are dot-separated with list indices in brackets, e.g.
// "RN1.details.tasks.scheduling.deliveryWindowDisplay" or "RN1.order.items[0]".
// ignore takes glob patterns ("*" matches anything, including dots).
func Diff(old, new any, ignore []string) []Change {
	var res []*regexp.Regexp
	for _, p := range ignore {
		res = append(res, Glob(p))
	}
	var out []Change
	walk(old, new, "", res, &out)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func walk(old, new any, path string, ignore []*regexp.Regexp, out *[]Change) {
	if path != "" {
		for _, re := range ignore {
			if re.MatchString(path) {
				return
			}
		}
	}
	om, oIsMap := old.(map[string]any)
	nm, nIsMap := new.(map[string]any)
	ol, oIsList := old.([]any)
	nl, nIsList := new.([]any)
	switch {
	case oIsMap && nIsMap:
		keys := map[string]bool{}
		for k := range om {
			keys[k] = true
		}
		for k := range nm {
			keys[k] = true
		}
		for k := range keys {
			child := k
			if path != "" {
				child = path + "." + k
			}
			walk(get(om, k), get(nm, k), child, ignore, out)
		}
	case oIsList && nIsList:
		for i := 0; i < max(len(ol), len(nl)); i++ {
			var o, n any = missing{}, missing{}
			if i < len(ol) {
				o = ol[i]
			}
			if i < len(nl) {
				n = nl[i]
			}
			walk(o, n, fmt.Sprintf("%s[%d]", path, i), ignore, out)
		}
	case old == (missing{}):
		*out = append(*out, Change{Kind: Added, Path: path, New: new})
	case new == (missing{}):
		*out = append(*out, Change{Kind: Removed, Path: path, Old: old})
	case !reflect.DeepEqual(old, new):
		*out = append(*out, Change{Kind: Changed, Path: path, Old: old, New: new})
	}
}

func get(m map[string]any, k string) any {
	if v, ok := m[k]; ok {
		return v
	}
	return missing{}
}

// Glob compiles a shell-style pattern ("*", "?") into an anchored regexp.
func Glob(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// Format renders a JSON value compactly for change lines (max 120 chars).
func Format(v any) string {
	var s string
	switch x := v.(type) {
	case nil:
		s = "null"
	case string:
		s = strconv.Quote(x)
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		s = strconv.FormatBool(x)
	default:
		s = fmt.Sprint(x)
	}
	if len([]rune(s)) > 120 {
		s = string([]rune(s)[:119]) + "…"
	}
	return s
}
