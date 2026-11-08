// Package testutil provides a fake HTTP client and helpers for tests.
package testutil

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mauritzuph/tesla-order-cli/internal/httpx"
)

// Call is one recorded request.
type Call struct {
	Method  string
	URL     string
	Body    any
	Headers map[string]string
}

// Route answers requests whose URL starts with a prefix. Exactly one of the
// fields is used: Err, Func, or Value (marshalled to JSON).
type Route struct {
	Value any
	Err   error
	Func  func(url string, body any) any
}

// FakeHTTP routes requests by URL prefix (longest prefix wins).
type FakeHTTP struct {
	Routes map[string]Route
	Calls  []Call
}

func NewFakeHTTP(routes map[string]Route) *FakeHTTP {
	if routes == nil {
		routes = map[string]Route{}
	}
	return &FakeHTTP{Routes: routes}
}

func (f *FakeHTTP) PostForm(url string, form map[string]string) ([]byte, error) {
	return f.handle("POST", url, form, nil)
}

func (f *FakeHTTP) PostJSON(url string, payload any, headers map[string]string) ([]byte, error) {
	return f.handle("POST", url, payload, headers)
}

func (f *FakeHTTP) GetJSON(url string, headers map[string]string) ([]byte, error) {
	return f.handle("GET", url, nil, headers)
}

func (f *FakeHTTP) handle(method, url string, body any, headers map[string]string) ([]byte, error) {
	f.Calls = append(f.Calls, Call{method, url, body, headers})
	prefixes := make([]string, 0, len(f.Routes))
	for p := range f.Routes {
		prefixes = append(prefixes, p)
	}
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i]) > len(prefixes[j]) })
	for _, p := range prefixes {
		if !strings.HasPrefix(url, p) {
			continue
		}
		r := f.Routes[p]
		if r.Err != nil {
			return nil, r.Err
		}
		v := r.Value
		if r.Func != nil {
			v = r.Func(url, body)
		}
		if v == nil {
			return nil, nil
		}
		return json.Marshal(v)
	}
	return nil, &httpx.Error{Status: 404, URL: url, Message: "no fake route"}
}

// CallsTo returns the calls whose URL starts with prefix.
func (f *FakeHTTP) CallsTo(prefix string) []Call {
	var out []Call
	for _, c := range f.Calls {
		if strings.HasPrefix(c.URL, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// JWT builds an unsigned JWT with the given exp claim.
func JWT(exp float64) string {
	part := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return fmt.Sprintf("%s.%s.sig", part(map[string]any{"alg": "none"}), part(map[string]any{"exp": exp}))
}

// Entry is a made-up order shaped like a real 2026 /tasks response.
func Entry() map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(entryJSON), &m); err != nil {
		panic(err)
	}
	return m
}

const entryJSON = `{
  "order": {"referenceNumber": "RN1", "orderStatus": "BOOKED", "orderSubstatus": "_Z",
            "modelCode": "m3", "countryCode": "DE", "isUsed": false, "isB2b": false,
            "mktOptions": "APBS,MDL3,W38A"},
  "details": {
    "state": {"tabPadding": 8},
    "strings": {"back": "Back"},
    "tasks": {
      "financing": {"order": 3, "complete": false, "enabled": true, "status": "DEFAULT",
                    "card": {"title": "Credit Decision Pending", "subtitle": "Pending"}},
      "deliveryDetails": {"order": 1, "complete": true, "enabled": true, "pickUpZipCode": "12345",
                          "customerType": "PRIVATE", "card": {"title": "Complete"}},
      "finalPayment": {"order": 8, "complete": true, "status": "PAYMENT_COMPLETE", "amountDue": -250,
                       "amountSent": 0, "currencyFormat": {"currencyCode": "EUR"}},
      "scheduling": {"order": 5, "deliveryType": "PICKUP_SERVICE_CENTER",
                     "deliveryAddressTitle": "Somewhere Center", "deliveryWindowDisplay": null,
                     "readyToAccept": true, "selfSchedulingUrl": "https://www.tesla.com/schedule/RN1"},
      "tradeIn": {"order": 2, "tradeInIntent": "No", "orderPlacedDate": "2026-11-06T09:43:37"},
      "registration": {"order": 4, "orderType": "TESLA_LENDING", "currentStep": "REG_TYPE",
                       "alertStatuses": {"orderType": "CREDIT_APPLICATION_RECEIVED"}},
      "deliveryAcceptance": {"order": 10, "model": "Model 3", "vehicleIsReady": true},
      "newTaskNobodyKnows": {"complete": false}
    }
  }
}`
