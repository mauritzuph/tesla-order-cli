package summary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mauritzuph/tesla-order-cli/internal/locations"
	"github.com/mauritzuph/tesla-order-cli/internal/ordered"
	"github.com/mauritzuph/tesla-order-cli/internal/testutil"
)

func TestSummaryReadsCurrentFieldLocations(t *testing.T) {
	s := Summarize(testutil.Entry())
	checks := map[[2]string]any{
		{"order", "model"}:                "Model 3",
		{"order", "order_placed"}:         "2026-11-06T09:43:37",
		{"delivery", "location"}:          "Somewhere Center",
		{"delivery", "pickup_zip_code"}:   "12345",
		{"delivery", "vehicle_ready"}:     true,
		{"payment", "financing_status"}:   "Credit Decision Pending",
		{"payment", "credit_application"}: "CREDIT_APPLICATION_RECEIVED",
		{"registration", "customer_type"}: "PRIVATE",
	}
	if s.ReferenceNumber != "RN1" {
		t.Errorf("ref %q", s.ReferenceNumber)
	}
	for k, want := range checks {
		if got := s.Value(k[0], k[1]); got != want {
			t.Errorf("%v = %v, want %v", k, got, want)
		}
	}
	if m, ok := s.Value("payment", "amount_due").(Money); !ok || m.Amount != -250.0 || m.Currency != "EUR" {
		t.Errorf("amount_due %v", s.Value("payment", "amount_due"))
	}
	codes := s.Value("order", "option_codes").([]any)
	if len(codes) != 3 || codes[0] != "APBS" {
		t.Errorf("option codes %v", codes)
	}
	// W38A has no built-in label, so only the known codes are decoded
	labels := s.Value("order", "options").(ordered.Map)
	if len(labels) != 2 || labels[0].Key != "APBS" || labels[0].Value != "Autopilot" || labels[1].Value != "Model 3" {
		t.Errorf("options %v", labels)
	}
}

func TestTasksInAppOrderIncludingUnknown(t *testing.T) {
	tasks := Summarize(testutil.Entry()).Tasks
	if tasks[0].ID != "deliveryDetails" || tasks[1].ID != "tradeIn" || tasks[2].ID != "financing" {
		t.Errorf("order: %v %v %v", tasks[0].ID, tasks[1].ID, tasks[2].ID)
	}
	last := tasks[len(tasks)-1]
	if last.ID != "newTaskNobodyKnows" || last.Name != "New task nobody knows" || last.Order != nil {
		t.Errorf("unknown task: %+v", last)
	}
}

func TestLegacyOrderDetailsLocations(t *testing.T) {
	entry := map[string]any{
		"order": map[string]any{"referenceNumber": "RN2", "modelCode": "my", "vin": nil},
		"details": map[string]any{"tasks": map[string]any{
			"registration": map[string]any{"orderDetails": map[string]any{
				"reservationDate": "2025-01-01", "vehicleOdometer": 12.0, "vehicleOdometerType": "KM", "vin": "5YJ3E",
			}},
			"finalPayment": map[string]any{"data": map[string]any{"etaToDeliveryCenter": "2025-02-01"}},
		}},
	}
	s := Summarize(entry)
	for k, want := range map[[2]string]any{
		{"order", "model"}: "Model Y", {"order", "vin"}: "5YJ3E", {"order", "reservation_date"}: "2025-01-01",
		{"delivery", "odometer"}: "12 KM", {"delivery", "eta_to_delivery_center"}: "2025-02-01",
	} {
		if got := s.Value(k[0], k[1]); got != want {
			t.Errorf("%v = %v, want %v", k, got, want)
		}
	}
	// orderDetails under deliveryDetails (reported in an upstream PR)
	entry["details"] = map[string]any{"tasks": map[string]any{"deliveryDetails": map[string]any{
		"orderDetails": map[string]any{"orderBookedDate": "x"}}}}
	if got := Summarize(entry).Value("order", "order_booked_date"); got != "x" {
		t.Errorf("got %v", got)
	}
}

func TestRoutingLocationName(t *testing.T) {
	path := filepath.Join(t.TempDir(), locations.FileName)
	if err := os.WriteFile(path, []byte(`{"1234": {"display_name": "Somewhere Center", "address": {"city": "Berlin"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := locations.Load(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locations.Load("") })
	entry := map[string]any{"details": map[string]any{"tasks": map[string]any{"registration": map[string]any{
		"orderDetails": map[string]any{"vehicleRoutingLocation": 1234.0}}}}}
	s := Summarize(entry)
	for key, want := range map[string]any{
		"routing_location": 1234.0, "routing_location_name": "Somewhere Center",
		"routing_location_address": "Berlin", "routing_location_phone": nil,
	} {
		if got := s.Value("delivery", key); got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
}

func TestFinancingDetails(t *testing.T) {
	entry := map[string]any{"details": map[string]any{"tasks": map[string]any{
		"agreements": map[string]any{"teslaFinanceDetails": map[string]any{"partnerName": "Some Bank"}},
		"finalPayment": map[string]any{"currencyFormat": map[string]any{"currencyCode": "EUR"}, "data": map[string]any{
			"amountDueFinancier": 40000.0,
			"financingDetails": map[string]any{"financialProductType": "OPERATIONAL_LEASE", "teslaFinanceDetails": map[string]any{
				"financePartnerName": "Other Bank", "monthlyPayment": 499.0, "termsInMonths": 48.0,
				"interestRate": 3.99, "mileage": 15000.0, "approvedLoanAmount": 41000.0,
			}},
		}},
	}}}
	s := Summarize(entry)
	for key, want := range map[string]any{
		"finance_product": "OPERATIONAL_LEASE", "finance_partner": "Some Bank", "term_months": 48.0,
		"interest_rate": 3.99, "mileage_per_year": 15000.0,
		"monthly_payment": Money{499.0, "EUR"}, "approved_amount": Money{41000.0, "EUR"}, "financed_amount": Money{40000.0, "EUR"},
	} {
		if got := s.Value("payment", key); got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}

	cash := map[string]any{"details": map[string]any{"tasks": map[string]any{"finalPayment": map[string]any{"data": map[string]any{
		"accountBalance": 0.0, "paymentDetails": []any{map[string]any{"amountPaid": 45000.0, "paymentType": "WIRE"}},
	}}}}}
	s = Summarize(cash)
	if s.Value("payment", "payment_method") != "WIRE" || s.Value("payment", "amount_paid") != (Money{45000.0, nil}) ||
		s.Value("payment", "account_balance") != (Money{0.0, nil}) || s.Value("payment", "interest_rate") != nil {
		t.Errorf("cash: %+v", s.Section("payment"))
	}
}

func TestDecodedVIN(t *testing.T) {
	s := Summarize(map[string]any{"order": map[string]any{"vin": "LRW3F7FS1PC000042"}})
	for key, want := range map[string]any{
		"factory": "Shanghai, China", "model_year": 2023, "steering": "Right-hand drive", "serial_number": "000042",
	} {
		if got := s.Value("order", key); got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	// nothing decoded until a full VIN is assigned
	for _, v := range []any{nil, "5YJ3E"} {
		s := Summarize(map[string]any{"order": map[string]any{"vin": v}})
		for _, key := range []string{"factory", "model_year", "steering", "serial_number"} {
			if got := s.Value("order", key); got != nil {
				t.Errorf("%v: %s = %v", v, key, got)
			}
		}
	}
}

func TestLikelyTransport(t *testing.T) {
	for _, c := range []struct {
		vin, country string
		want         any
	}{
		{"LRW3F7FS1PC000042", "AU", "Ship (CN → AU)"},
		{"LRWYGCEK1PC000042", "DE", "Ship (CN → DE)"},
		{"XP7YGCEK2RB000042", "nl", "Truck or train"},
		{"XP7YGCEK2RB000042", "IE", "Ship (DE → IE)"},
		{"7SAYGDEE1TA000042", "CA", "Truck or train"},
		{"7SAYGDEE1TA000042", "KR", "Ship (US → KR)"},
		{"7SAYGDEE1TA000042", "", nil},
		{"5YJ3E1EA1PZ000001", "DE", nil}, // unknown factory
	} {
		s := Summarize(map[string]any{"order": map[string]any{"vin": c.vin, "countryCode": c.country}})
		if got := s.Value("delivery", "likely_transport"); got != c.want {
			t.Errorf("%s/%s: got %v, want %v", c.vin, c.country, got, c.want)
		}
	}
}

func TestEmptyEntry(t *testing.T) {
	s := Summarize(map[string]any{"order": map[string]any{}, "details": nil})
	if len(s.Tasks) != 0 || s.Value("order", "model") != nil {
		t.Errorf("got %+v", s)
	}
}

func TestDataFieldsSkipUITextAndEmpties(t *testing.T) {
	fields := map[string]any{}
	for _, f := range DataFields(testutil.Entry()) {
		fields[f.Path] = f.Value
	}
	if fields["order.mktOptions"] != "APBS,MDL3,W38A" || fields["details.tasks.finalPayment.amountDue"] != -250.0 {
		t.Errorf("missing data fields: %v", fields)
	}
	for k := range fields {
		if strings.Contains(k, ".card") || strings.Contains(k, "strings") || strings.Contains(k, "state") ||
			strings.HasSuffix(k, "selfSchedulingUrl") || strings.HasSuffix(k, "deliveryWindowDisplay") {
			t.Errorf("should be skipped: %s", k)
		}
	}
}

func TestMapIsOrderedJSON(t *testing.T) {
	data, err := json.Marshal(Summarize(testutil.Entry()).Map())
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, key := range []string{`"reference_number"`, `"order"`, `"delivery"`, `"payment"`, `"registration"`, `"tasks"`} {
		idx := strings.Index(s, key)
		if idx < 0 {
			t.Fatalf("missing %s", key)
		}
		s = s[idx:]
	}
}

func TestHumanize(t *testing.T) {
	if got := Humanize("newTaskNobodyKnows"); got != "New task nobody knows" {
		t.Errorf("got %q", got)
	}
}
