// Package render prints summaries as pretty, table, json or yaml.
package render

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/mauritzuph/tesla-order-cli/internal/ordered"
	"github.com/mauritzuph/tesla-order-cli/internal/summary"
)

var Formats = []string{"pretty", "table", "json", "yaml"}

// ReferralURL is the author's Tesla referral link, shown below pretty output.
const ReferralURL = "https://www.tesla.com/de_de/referral/mauritz69012"

var (
	isoDateTime = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2})(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?$`)
	enumValue   = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$|^[A-Z]{4,}$`)
)

// Document is what gets rendered: all orders plus when they were fetched.
type Document struct {
	FetchedAt string
	Orders    []summary.Summary
}

// Build summarizes all orders (sorted by reference number).
func Build(orders map[string]any, fetchedAt string, includeAll bool) Document {
	refs := make([]string, 0, len(orders))
	for ref := range orders {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	doc := Document{FetchedAt: fetchedAt, Orders: []summary.Summary{}}
	for _, ref := range refs {
		entry, _ := orders[ref].(map[string]any)
		s := summary.Summarize(entry)
		if includeAll {
			s.DataFields = summary.DataFields(entry)
		}
		doc.Orders = append(doc.Orders, s)
	}
	return doc
}

func (d Document) Map() ordered.Map {
	orders := make([]ordered.Map, 0, len(d.Orders))
	for _, s := range d.Orders {
		orders = append(orders, s.Map())
	}
	var fetched any
	if d.FetchedAt != "" {
		fetched = d.FetchedAt
	}
	return ordered.Map{{Key: "fetched_at", Value: fetched}, {Key: "orders", Value: orders}}
}

// Render writes doc to w in the given format.
func Render(w io.Writer, doc Document, format string) error {
	switch format {
	case "json":
		data, err := ordered.MarshalIndent(doc.Map())
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	case "yaml":
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		if err := enc.Encode(doc.Map()); err != nil {
			return err
		}
		return enc.Close()
	case "pretty", "table":
	default:
		return fmt.Errorf("unknown format %q", format)
	}

	r := lipgloss.NewRenderer(w)
	if len(doc.Orders) == 0 {
		_, err := fmt.Fprintln(w, "No orders found on this Tesla account.")
		return err
	}
	if doc.FetchedAt != "" {
		fmt.Fprintln(w, r.NewStyle().Faint(true).Render("Fetched "+FormatValue("fetched_at", doc.FetchedAt, false)))
	}
	width := termWidth(w)
	for _, s := range doc.Orders {
		if format == "pretty" {
			fmt.Fprintln(w, pretty(r, s, width))
		} else {
			fmt.Fprintln(w, tables(r, s, width))
		}
	}
	if format == "pretty" {
		fmt.Fprintln(w, referralHint(r))
	}
	return nil
}

// referralHint is a subtle one-line footer for pretty output only; table, json
// and yaml stay free of it so scripts aren't affected.
func referralHint(r *lipgloss.Renderer) string {
	return r.NewStyle().Faint(true).Render("Ordering a Tesla? Support this tool with my referral link: ") +
		r.NewStyle().Foreground(lipgloss.Color("6")).Render(ReferralURL)
}

func termWidth(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 40 {
			return min(width, 120)
		}
	}
	return 100
}

// pretty ---------------------------------------------------------------------

func pretty(r *lipgloss.Renderer, s summary.Summary, width int) string {
	heading := r.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	label := r.NewStyle().Faint(true).Width(24)
	inner := width - 6 // border + padding
	value := r.NewStyle().Width(inner - 26)

	var parts []string
	for _, sec := range s.Sections {
		var rows []string
		for _, f := range visible(sec.Fields) {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top,
				label.Render(f.Label), "  ", value.Render(FormatValue(f.Key, f.Value, true))))
		}
		if len(rows) > 0 {
			parts = append(parts, heading.Render(sec.Title)+"\n"+strings.Join(rows, "\n"))
		}
	}
	parts = append(parts, heading.Render("Delivery checklist")+"\n"+checklist(r, s.Tasks, inner))
	if s.DataFields != nil {
		parts = append(parts, heading.Render("All data fields")+"\n"+fieldsTable(r, s.DataFields, inner))
	}

	model, _ := s.Value("order", "model").(string)
	title := s.ReferenceNumber
	if model != "" {
		title = model + " · " + s.ReferenceNumber
	}
	status := joinNonEmpty(" / ", s.Value("order", "status"), s.Value("order", "substatus"))
	header := r.NewStyle().Bold(true).Render(title)
	if status != "" {
		gap := max(1, inner-lipgloss.Width(header)-lipgloss.Width(status))
		header += strings.Repeat(" ", gap) + r.NewStyle().Faint(true).Render(status)
	}

	box := r.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Width(width - 2)
	return box.Render(header + "\n\n" + strings.Join(parts, "\n\n"))
}

func checklist(r *lipgloss.Renderer, tasks []summary.Task, width int) string {
	name := r.NewStyle().Bold(true).Width(24)
	detailStyle := r.NewStyle().Width(width - 28)
	var rows []string
	for _, t := range tasks {
		icon := r.NewStyle().Faint(true).Render("○") // not open yet
		switch {
		case t.Complete == true:
			icon = r.NewStyle().Foreground(lipgloss.Color("2")).Render("✓")
		case t.Enabled == true:
			icon = r.NewStyle().Foreground(lipgloss.Color("3")).Render("●") // something to do now
		}
		detail := str(t.Title)
		if detail == "" {
			detail = FormatValue("status", t.Status, true)
		}
		if sub := str(t.Subtitle); sub != "" && sub != str(t.Title) {
			detail += r.NewStyle().Faint(true).Render(" · " + sub)
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, icon, "  ", name.Render(t.Name), detailStyle.Render(detail)))
	}
	return strings.Join(rows, "\n")
}

// table ----------------------------------------------------------------------

func tables(r *lipgloss.Renderer, s summary.Summary, width int) string {
	head := r.NewStyle().Bold(true).Padding(0, 1)
	cell := r.NewStyle().Padding(0, 1)

	fields := table.New().Border(lipgloss.NormalBorder()).Width(width).
		Headers("Section", "Field", "Value").
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return head
			}
			return cell
		})
	for _, sec := range s.Sections {
		firstRow := true
		for _, f := range visible(sec.Fields) {
			name := ""
			if firstRow {
				name = sec.Key
			}
			fields.Row(name, f.Label, FormatValue(f.Key, f.Value, false))
			firstRow = false
		}
	}

	tasks := table.New().Border(lipgloss.NormalBorder()).Width(width).
		Headers("#", "Task", "Complete", "Open", "Status", "Title", "Subtitle").
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return head
			}
			return cell
		})
	for _, t := range s.Tasks {
		tasks.Row(FormatValue("order", t.Order, false), t.Name, FormatValue("", t.Complete, false),
			FormatValue("", t.Enabled, false), FormatValue("", t.Status, false),
			FormatValue("", t.Title, false), FormatValue("", t.Subtitle, false))
	}

	out := "Order " + s.ReferenceNumber + "\n" + fields.Render() + "\nDelivery checklist\n" + tasks.Render()
	if s.DataFields != nil {
		out += "\nAll data fields\n" + fieldsTable(r, s.DataFields, width)
	}
	return out
}

func fieldsTable(r *lipgloss.Renderer, fields []summary.DataField, width int) string {
	t := table.New().Border(lipgloss.HiddenBorder()).Width(width).Headers("Path", "Value").
		StyleFunc(func(row, col int) lipgloss.Style {
			st := r.NewStyle().PaddingRight(2)
			if row == table.HeaderRow {
				return st.Bold(true)
			}
			if col == 0 {
				return st.Faint(true)
			}
			return st
		})
	for _, f := range fields {
		t.Row(f.Path, FormatValue(f.Path, f.Value, false))
	}
	return t.Render()
}

// values ---------------------------------------------------------------------

func visible(fields []summary.Field) []summary.Field {
	var out []summary.Field
	for _, f := range fields {
		if f.AlwaysShow || !isEmpty(f.Value) {
			out = append(out, f)
		}
	}
	return out
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// FormatValue renders a value for pretty/table output. humanize turns enum
// values like PICKUP_SERVICE_CENTER into "Pickup service center".
func FormatValue(key string, v any, humanize bool) string {
	switch x := v.(type) {
	case nil:
		return "—"
	case bool:
		if x {
			return "yes"
		}
		return "no"
	case float64:
		return number(x)
	case summary.Money:
		amount := FormatValue(key, x.Amount, false)
		if c, ok := x.Currency.(string); ok && c != "" {
			return amount + " " + c
		}
		return amount
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+": "+FormatValue(k, x[k], humanize))
		}
		return strings.Join(parts, ", ")
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, FormatValue(key, e, humanize))
		}
		return strings.Join(parts, ", ")
	case string:
		if x == "" {
			return "—"
		}
		if m := isoDateTime.FindStringSubmatch(x); m != nil {
			return m[1] + " " + m[2]
		}
		if humanize && key != "option_codes" && enumValue.MatchString(x) {
			s := strings.ToLower(strings.ReplaceAll(x, "_", " "))
			return strings.ToUpper(s[:1]) + s[1:]
		}
		return x
	}
	return fmt.Sprint(v)
}

// number formats with thousands separators: 1234567 -> "1,234,567", 12.5 -> "12.5".
func number(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	intPart, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if hasFrac {
		return sign + b.String() + "." + frac
	}
	return sign + b.String()
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func joinNonEmpty(sep string, values ...any) string {
	var parts []string
	for _, v := range values {
		if s := str(v); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, sep)
}
