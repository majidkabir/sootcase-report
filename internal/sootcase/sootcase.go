// Package sootcase calls the Sootcase API's report endpoint and holds the
// parts of its answer the PDF uses. Field names follow the API's JSON
// (see https://github.com/majidkabir/sootcase, pkg/report).
package sootcase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultURL is the free public Sootcase service.
const DefaultURL = "https://sootcase.org"

// Report is /v1/report's answer with ?results=true. Emissions are kg CO2e.
type Report struct {
	Title        string    `json:"title"`
	Organisation string    `json:"organisation"`
	Period       Period    `json:"period"`
	Level        string    `json:"level"`
	Statement    string    `json:"statement"`
	Reference    string    `json:"reference"`
	Datasets     []string  `json:"datasets"`
	Generated    time.Time `json:"generated"`
	Coverage     Coverage  `json:"coverage"`
	Total        Total     `json:"total"`
	Modes        []Mode    `json:"modes"`
	Hubs         []Hub     `json:"hubs"`
	Energy       []Energy  `json:"energy_carriers"`
	Data         Data      `json:"data_quality"`
	DAFs         []DAF     `json:"distance_adjustments"`
	GHG          GHG       `json:"ghg_protocol"`
	Notes        []string  `json:"notes"`
	Results      []Item    `json:"results"`
}

type Period struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type Coverage struct {
	Shipments       int           `json:"shipments"`
	Calculated      int           `json:"calculated"`
	Failed          int           `json:"failed"`
	Failures        []Failure     `json:"failures"`
	FailureReasons  []ReasonCount `json:"failure_reasons"`
	RetryableFailed int           `json:"retryable_failed"`
	Legs            int           `json:"legs"`
	HubStops        int           `json:"hub_stops"`
	WeightT         float64       `json:"weight_t"`
	AddedRoadLegs   int           `json:"added_road_legs"`
	AddedRoadKm     float64       `json:"added_road_km"`
	AddedShipments  int           `json:"added_road_leg_shipments"`
	Warned          int           `json:"warned_shipments"`
	Warnings        []ReasonCount `json:"warnings"`
}

type Failure struct {
	ShipmentID string `json:"shipment_id"`
	Error      string `json:"error"`
	Retryable  bool   `json:"retryable"`
}

type ReasonCount struct {
	Reason    string `json:"reason"`
	Shipments int    `json:"shipments"`
}

type Total struct {
	WTWKg          float64            `json:"wtw_kg"`
	TransportWTWKg float64            `json:"transport_wtw_kg"`
	TTWKg          float64            `json:"ttw_kg"`
	WTTKg          float64            `json:"wtt_kg"`
	HubWTWKg       float64            `json:"hub_wtw_kg"`
	TonneKm        float64            `json:"tonne_km"`
	IntensityG     float64            `json:"intensity_g_per_tkm"`
	BiogenicCO2Kg  float64            `json:"biogenic_co2_kg"`
	DistanceTypes  map[string]float64 `json:"distance_types"`
}

type Mode struct {
	Mode          string             `json:"mode"`
	Legs          int                `json:"legs"`
	TonneKm       float64            `json:"tonne_km"`
	TTWKg         float64            `json:"ttw_kg"`
	WTTKg         float64            `json:"wtt_kg"`
	WTWKg         float64            `json:"wtw_kg"`
	IntensityG    float64            `json:"intensity_g_per_tkm"`
	DistanceTypes map[string]float64 `json:"distance_types"`
}

type Hub struct {
	HubType     string  `json:"hub_type"`
	Stops       int     `json:"stops"`
	Tonnes      float64 `json:"tonnes"`
	WTWKg       float64 `json:"wtw_kg"`
	IntensityGT float64 `json:"intensity_g_per_t"`
}

type Energy struct {
	Carrier string  `json:"carrier"`
	TTWKg   float64 `json:"ttw_kg"`
	WTTKg   float64 `json:"wtt_kg"`
	WTWKg   float64 `json:"wtw_kg"`
	Share   float64 `json:"share"`
}

type Data struct {
	Primary        float64  `json:"primary_share"`
	Modelled       float64  `json:"modelled_share"`
	Default        float64  `json:"default_share"`
	DefaultSources []Source `json:"default_sources"`
	Models         []Model  `json:"models"`
}

type Source struct {
	Source string  `json:"source"`
	WTWKg  float64 `json:"wtw_kg"`
	Share  float64 `json:"share"`
}

type Model struct {
	Model      string            `json:"model"`
	Legs       int               `json:"legs"`
	WTWKg      float64           `json:"wtw_kg"`
	Parameters map[string]string `json:"parameters"`
}

type DAF struct {
	Source string  `json:"source"`
	Legs   int     `json:"legs"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

type GHG struct {
	Scope3Category  int     `json:"scope3_category"`
	Scope3ShipperKg float64 `json:"scope3_shipper_kg"`
	Scope1Kg        float64 `json:"operator_scope1_kg"`
	Scope2Kg        float64 `json:"operator_scope2_kg"`
	Scope3Cat3Kg    float64 `json:"operator_scope3_cat3_kg"`
	HubsKg          float64 `json:"hubs_kg"`
}

// Item is one shipment's result or failure.
type Item struct {
	ShipmentID string  `json:"shipment_id"`
	Result     *Result `json:"result"`
	Error      string  `json:"error"`
	Retryable  bool    `json:"retryable"`
}

type Result struct {
	WeightKg   float64 `json:"weight_kg"`
	TonneKm    float64 `json:"tonne_km"`
	TTWKg      float64 `json:"ttw_kg"`
	WTTKg      float64 `json:"wtt_kg"`
	WTWKg      float64 `json:"wtw_kg"`
	IntensityG float64 `json:"intensity_g_per_tkm"`
	Legs       []Leg   `json:"legs"`
}

type Leg struct {
	Mode       string  `json:"mode"`
	DistanceKm float64 `json:"distance_km"`
	From       *Place  `json:"from"`
	To         *Place  `json:"to"`
}

type Place struct {
	Name string `json:"name"`
	Code string `json:"code"`
}

// Options are the report details sent with the CSV.
type Options struct {
	Title          string
	Organisation   string
	PeriodFrom     string // YYYY-MM-DD
	PeriodTo       string
	Reference      string
	DeclareISO     bool
	Scope3Category int // 4 or 9, 0 for none
	NoRoadLegs     bool
}

// Client calls a Sootcase server.
type Client struct {
	BaseURL string // DefaultURL when empty
	APIKey  string // sent as a bearer token when set
	HTTP    *http.Client
}

// Error is a failed API call.
type Error struct {
	Status     int
	Message    string
	RetryAfter string
}

func (e *Error) Error() string {
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Sprintf("sootcase: not allowed (%s); get an API key at sootcase.org and pass it with -key or SOOTCASE_API_KEY", e.Message)
	case http.StatusTooManyRequests:
		return "sootcase: quota reached: " + e.Message
	case http.StatusServiceUnavailable:
		s := "sootcase: busy or a routing service didn't answer; try again"
		if e.RetryAfter != "" {
			s += " in " + e.RetryAfter + " s"
		}
		return s
	}
	return fmt.Sprintf("sootcase: HTTP %d: %s", e.Status, e.Message)
}

// Report sends a shipments CSV (Sootcase's batch columns) to /v1/report and
// returns the report with every shipment's result.
func (c *Client) Report(ctx context.Context, csv io.Reader, o Options) (*Report, error) {
	r, _, err := c.ReportRaw(ctx, csv, o)
	return r, err
}

// ReportRaw is Report, also returning the API's JSON as it came.
func (c *Client) ReportRaw(ctx context.Context, csv io.Reader, o Options) (*Report, []byte, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultURL
	}
	q := url.Values{"results": {"true"}}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("title", o.Title)
	set("organisation", o.Organisation)
	set("period_from", o.PeriodFrom)
	set("period_to", o.PeriodTo)
	set("reference", o.Reference)
	if o.DeclareISO {
		q.Set("declare_iso", "true")
	}
	if o.Scope3Category != 0 {
		q.Set("scope3_category", strconv.Itoa(o.Scope3Category))
	}
	if o.NoRoadLegs {
		q.Set("add_road_legs", "false")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/report?"+q.Encode(), csv)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "text/csv")
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(body))
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return nil, nil, &Error{Status: resp.StatusCode, Message: msg, RetryAfter: resp.Header.Get("Retry-After")}
	}
	r, err := Decode(body)
	return r, body, err
}

// Decode reads a saved /v1/report JSON answer.
func Decode(b []byte) (*Report, error) {
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("reading the report: %w", err)
	}
	if r.Level == "" && r.Coverage.Shipments == 0 {
		return nil, errors.New("reading the report: this isn't a Sootcase /v1/report answer")
	}
	return &r, nil
}
