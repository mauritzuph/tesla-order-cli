// Package api wraps Tesla's order endpoints.
package api

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/mauritzuph/tesla-order-cli/internal/httpx"
)

const (
	OrdersURL = "https://owner-api.teslamotors.com/api/1/users/orders"
	TasksURL  = "https://akamai-apigateway-vfx.tesla.com/tasks"
	// The tasks endpoint wants an app version but does not check it strictly.
	appVersion = "9.99.9-9999"
)

type API struct {
	HTTP     httpx.Client
	Token    string
	Country  string
	Language string
}

func (a API) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + a.Token}
}

// ListOrders returns the raw order objects.
func (a API) ListOrders() ([]map[string]any, error) {
	body, err := a.HTTP.GetJSON(OrdersURL, a.headers())
	if err != nil {
		return nil, err
	}
	var r struct {
		Response []map[string]any `json:"response"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("unexpected orders response: %w", err)
	}
	return r.Response, nil
}

// OrderDetails returns the raw /tasks response for one order.
func (a API) OrderDetails(ref string) (map[string]any, error) {
	lang, country := a.Language, a.Country
	if lang == "" {
		lang = "en"
	}
	if country == "" {
		country = "DE"
	}
	q := url.Values{
		"deviceLanguage":  {lang},
		"deviceCountry":   {country},
		"referenceNumber": {ref},
		"appVersion":      {appVersion},
	}
	body, err := a.HTTP.GetJSON(TasksURL+"?"+q.Encode(), a.headers())
	if err != nil {
		return nil, err
	}
	details := map[string]any{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &details); err != nil {
			return nil, fmt.Errorf("unexpected tasks response: %w", err)
		}
	}
	return details, nil
}

// FetchSnapshot returns all orders with details, keyed by reference number:
// {ref: {"order": {...}, "details": {...}}}.
func (a API) FetchSnapshot() (map[string]any, error) {
	orders, err := a.ListOrders()
	if err != nil {
		return nil, err
	}
	snap := make(map[string]any, len(orders))
	for _, o := range orders {
		ref, _ := o["referenceNumber"].(string)
		details, err := a.OrderDetails(ref)
		if err != nil {
			return nil, err
		}
		snap[ref] = map[string]any{"order": o, "details": details}
	}
	return snap, nil
}
