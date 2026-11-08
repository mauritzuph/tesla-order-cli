// Package summary turns a raw order entry ({"order": ..., "details": ...}) into
// a structured summary.
//
// The /tasks response is large and mostly UI text. The useful data is spread
// across "tasks" (the delivery checklist in the Tesla app). Field locations
// differ between accounts and order stages, so every value is looked up in all
// known places, and anything missing is nil.
package summary

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/mauritzuph/tesla-order-cli/internal/ordered"
)

var modelNames = map[string]string{
	"m3": "Model 3", "my": "Model Y", "ms": "Model S", "mx": "Model X", "ct": "Cybertruck",
}

var taskNames = map[string]string{
	"deliveryDetails":    "Delivery details",
	"tradeIn":            "Trade-in",
	"financing":          "Financing",
	"registration":       "Registration",
	"scheduling":         "Delivery appointment",
	"carbonCredit":       "Carbon credit",
	"finalPayment":       "Final payment",
	"deliveryAcceptance": "Delivery day",
}

// uiKey matches raw paths that are UI text or app layout, not order data.
var uiKey = regexp.MustCompile(`(?i)(^|\.)(strings|copy|card|cta|buttons?|labels?|translations|header|footer|modal|tooltip` +
	`|placeholder|disclaimer|faq|banner|icon|image\w*|\w*Url|\w*Image\w*|\w*CropParams)(\.|$|\[)`)

// Field is one summary value. AlwaysShow fields are displayed even when empty,
// because "not yet" is the information (VIN, delivery window, ...).
type Field struct {
	Key        string
	Label      string
	Value      any
	AlwaysShow bool
}

type Section struct {
	Key    string
	Title  string
	Fields []Field
}

// Money is an amount with currency.
type Money struct {
	Amount   any
	Currency any
}

func (m Money) toMap() ordered.Map {
	return ordered.Map{{Key: "amount", Value: m.Amount}, {Key: "currency", Value: m.Currency}}
}
func (m Money) MarshalJSON() ([]byte, error) { return m.toMap().MarshalJSON() }
func (m Money) MarshalYAML() (any, error)    { return m.toMap().MarshalYAML() }

// Task is one entry of the app's delivery checklist.
type Task struct {
	ID       string `json:"id" yaml:"id"`
	Name     string `json:"name" yaml:"name"`
	Order    any    `json:"order" yaml:"order"`
	Complete any    `json:"complete" yaml:"complete"`
	Enabled  any    `json:"enabled" yaml:"enabled"`
	Required any    `json:"required" yaml:"required"`
	Status   any    `json:"status" yaml:"status"`
	Title    any    `json:"title" yaml:"title"`
	Subtitle any    `json:"subtitle" yaml:"subtitle"`
}

// DataField is one raw value, for --all.
type DataField struct {
	Path  string
	Value any
}

type Summary struct {
	ReferenceNumber string
	Sections        []Section
	Tasks           []Task
	DataFields      []DataField // only with --all
}

// Section returns the section with the given key.
func (s Summary) Section(key string) Section {
	for _, sec := range s.Sections {
		if sec.Key == key {
			return sec
		}
	}
	return Section{}
}

// Value returns a field value, e.g. Value("delivery", "location").
func (s Summary) Value(section, key string) any {
	for _, f := range s.Section(section).Fields {
		if f.Key == key {
			return f.Value
		}
	}
	return nil
}

// Map is the JSON/YAML representation.
func (s Summary) Map() ordered.Map {
	m := ordered.Map{{Key: "reference_number", Value: s.ReferenceNumber}}
	for _, sec := range s.Sections {
		fields := ordered.Map{}
		for _, f := range sec.Fields {
			fields = append(fields, ordered.KV{Key: f.Key, Value: f.Value})
		}
		m = append(m, ordered.KV{Key: sec.Key, Value: fields})
	}
	tasks := s.Tasks
	if tasks == nil {
		tasks = []Task{}
	}
	m = append(m, ordered.KV{Key: "tasks", Value: tasks})
	if s.DataFields != nil {
		fields := ordered.Map{}
		for _, f := range s.DataFields {
			fields = append(fields, ordered.KV{Key: f.Path, Value: f.Value})
		}
		m = append(m, ordered.KV{Key: "data_fields", Value: fields})
	}
	return m
}

// Summarize builds the summary of one order entry.
func Summarize(entry map[string]any) Summary {
	order := asMap(entry["order"])
	details := asMap(entry["details"])
	tasks := asMap(details["tasks"])
	task := func(name string, path ...string) any { return dig(tasks, append([]string{name}, path...)...) }

	// Older/other responses keep these under registration or deliveryDetails.
	orderInfo := asMap(first(task("deliveryDetails", "orderDetails"), task("registration", "orderDetails")))
	legacyPayment := asMap(task("finalPayment", "data"))
	currency := first(task("finalPayment", "currencyFormat", "currencyCode"), task("tradeIn", "currencyCode"))
	modelCode := first(order["modelCode"], task("scheduling", "modelCode"))

	var odometer any
	if o, ok := orderInfo["vehicleOdometer"]; ok && o != nil {
		odometer = strings.TrimSpace(fmt.Sprintf("%v %v", plain(o), plain(first(orderInfo["vehicleOdometerType"], ""))))
	}
	var optionCodes any
	if s, ok := order["mktOptions"].(string); ok && s != "" {
		var codes []any
		for _, c := range strings.Split(s, ",") {
			if c != "" {
				codes = append(codes, c)
			}
		}
		optionCodes = codes
	}
	var model any
	if code, ok := modelCode.(string); ok {
		if name, ok := modelNames[code]; ok {
			model = name
		}
	}
	model = first(task("deliveryAcceptance", "model"), model)

	ref, _ := order["referenceNumber"].(string)
	return Summary{
		ReferenceNumber: ref,
		Sections: []Section{
			{Key: "order", Title: "Order", Fields: []Field{
				{Key: "model", Label: "Model", Value: model},
				{Key: "model_code", Label: "Model code", Value: modelCode},
				{Key: "status", Label: "Status", Value: order["orderStatus"], AlwaysShow: true},
				{Key: "substatus", Label: "Substatus", Value: order["orderSubstatus"]},
				{Key: "vin", Label: "VIN", Value: first(order["vin"], orderInfo["vin"]), AlwaysShow: true},
				{Key: "order_placed", Label: "Order placed", Value: first(task("tradeIn", "orderPlacedDate"), orderInfo["orderPlacedDate"])},
				{Key: "reservation_date", Label: "Reserved", Value: orderInfo["reservationDate"]},
				{Key: "order_booked_date", Label: "Booked", Value: orderInfo["orderBookedDate"]},
				{Key: "country", Label: "Country", Value: order["countryCode"]},
				{Key: "used", Label: "Used vehicle", Value: order["isUsed"]},
				{Key: "business_order", Label: "Business order", Value: order["isB2b"]},
				{Key: "option_codes", Label: "Option codes", Value: optionCodes},
			}},
			{Key: "delivery", Title: "Delivery", Fields: []Field{
				{Key: "type", Label: "Type", Value: first(task("scheduling", "deliveryType"), task("deliveryAcceptance", "deliveryType"))},
				{Key: "location", Label: "Location", Value: task("scheduling", "deliveryAddressTitle")},
				{Key: "address", Label: "Address", Value: first(task("scheduling", "deliveryAddressString"))},
				{Key: "pickup_zip_code", Label: "Pickup ZIP code", Value: task("deliveryDetails", "pickUpZipCode")},
				{Key: "window", Label: "Delivery window", Value: task("scheduling", "deliveryWindowDisplay"), AlwaysShow: true},
				{Key: "appointment", Label: "Appointment", Value: first(
					task("scheduling", "apptDateTimeAddressStr"),
					task("scheduling", "deliveryAppointmentDate"),
					task("deliveryAcceptance", "appointmentDate"),
				), AlwaysShow: true},
				{Key: "appointment_status", Label: "Appointment status", Value: first(task("scheduling", "appointmentStatusName"))},
				{Key: "self_scheduling_available", Label: "Self-scheduling", Value: task("scheduling", "isSelfSchedulingAvailable")},
				{Key: "self_scheduling_url", Label: "Self-scheduling link", Value: first(task("scheduling", "selfSchedulingUrl"))},
				{Key: "vehicle_ready", Label: "Vehicle ready", Value: task("deliveryAcceptance", "vehicleIsReady")},
				{Key: "ready_to_accept", Label: "Ready to accept", Value: task("scheduling", "readyToAccept")},
				{Key: "vehicle_located_at", Label: "Vehicle located", Value: task("deliveryAcceptance", "vehicleLocatedTimestamp")},
				{Key: "more_than_two_weeks_away", Label: "More than 2 weeks away", Value: task("scheduling", "isMoreThanTwoWeeks")},
				{Key: "title_status", Label: "Title status", Value: task("scheduling", "vehicleTitleStatus")},
				{Key: "eta_to_delivery_center", Label: "ETA to delivery center", Value: legacyPayment["etaToDeliveryCenter"]},
				{Key: "routing_location", Label: "Routing location", Value: orderInfo["vehicleRoutingLocation"]},
				{Key: "odometer", Label: "Odometer", Value: odometer},
			}},
			{Key: "payment", Title: "Payment", Fields: []Field{
				{Key: "status", Label: "Status", Value: task("finalPayment", "status"), AlwaysShow: true},
				{Key: "amount_due", Label: "Amount due", Value: money(task("finalPayment", "amountDue"), currency)},
				{Key: "amount_sent", Label: "Amount sent", Value: money(task("finalPayment", "amountSent"), currency)},
				{Key: "financing_type", Label: "Financing type", Value: task("registration", "orderType")},
				{Key: "financing_status", Label: "Financing", Value: first(task("financing", "card", "title"), task("financing", "status"))},
				{Key: "credit_application", Label: "Credit application", Value: task("registration", "alertStatuses", "orderType")},
				{Key: "trade_in", Label: "Trade-in", Value: task("tradeIn", "tradeInIntent")},
				{Key: "trade_in_status", Label: "Trade-in status", Value: task("tradeIn", "status")},
			}},
			{Key: "registration", Title: "Registration", Fields: []Field{
				{Key: "customer_type", Label: "Customer type", Value: first(task("registration", "customerType"), task("deliveryDetails", "customerType"))},
				{Key: "current_step", Label: "Current step", Value: task("registration", "currentStep")},
				{Key: "status", Label: "Status", Value: task("registration", "status"), AlwaysShow: true},
				{Key: "expected_registration_date", Label: "Expected registration", Value: task("registration", "expectedRegDate")},
				{Key: "status_flags", Label: "Status flags", Value: task("registration", "alertStatuses")},
			}},
		},
		Tasks: buildTasks(tasks),
	}
}

func buildTasks(tasks map[string]any) []Task {
	var out []Task
	for id, raw := range tasks {
		data, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		card := asMap(data["card"])
		name := taskNames[id]
		if name == "" {
			name = Humanize(id)
		}
		out = append(out, Task{
			ID: id, Name: name, Order: data["order"], Complete: data["complete"],
			Enabled: data["enabled"], Required: data["required"], Status: data["status"],
			Title: card["title"], Subtitle: card["subtitle"],
		})
	}
	// App order; tasks without an order go last.
	sort.Slice(out, func(i, j int) bool {
		oi, iok := out[i].Order.(float64)
		oj, jok := out[j].Order.(float64)
		if iok != jok {
			return iok
		}
		if oi != oj {
			return oi < oj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// DataFields lists every non-empty raw value except UI text, sorted by path.
func DataFields(entry map[string]any) []DataField {
	out := []DataField{}
	var walk func(v any, path string)
	walk = func(v any, path string) {
		if path != "" && uiKey.MatchString(path) {
			return
		}
		if strings.HasPrefix(path, "details.strings") || strings.HasPrefix(path, "details.state") {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				child := k
				if path != "" {
					child = path + "." + k
				}
				walk(x[k], child)
			}
		case []any:
			nested := false
			for _, e := range x {
				switch e.(type) {
				case map[string]any, []any:
					nested = true
				}
			}
			if !nested {
				if len(x) > 0 {
					out = append(out, DataField{path, x})
				}
				return
			}
			for i, e := range x {
				walk(e, fmt.Sprintf("%s[%d]", path, i))
			}
		default:
			if !empty(x) {
				out = append(out, DataField{path, x})
			}
		}
	}
	walk(entry, "")
	return out
}

// Humanize turns "newTaskName" into "New task name".
func Humanize(key string) string {
	var b strings.Builder
	for i, r := range key {
		switch {
		case r == '_':
			b.WriteRune(' ')
		case i > 0 && unicode.IsUpper(r):
			b.WriteRune(' ')
			b.WriteRune(unicode.ToLower(r))
		case i == 0:
			b.WriteRune(unicode.ToUpper(r))
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func money(amount, currency any) any {
	if amount == nil {
		return nil
	}
	return Money{Amount: amount, Currency: currency}
}

func dig(v any, path ...string) any {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// first returns the first value that is not nil or empty.
func first(values ...any) any {
	for _, v := range values {
		if !empty(v) {
			return v
		}
	}
	return nil
}

func empty(v any) bool {
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

// plain prints JSON numbers without exponent/decimals where possible.
func plain(v any) string {
	if f, ok := v.(float64); ok {
		return fmt.Sprintf("%v", trimFloat(f))
	}
	return fmt.Sprint(v)
}

func trimFloat(f float64) any {
	if f == float64(int64(f)) {
		return int64(f)
	}
	return f
}
