package sootcase

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestReportRequest(t *testing.T) {
	fixture, _ := os.ReadFile("../../testdata/report.json")
	var got *http.Request
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Write(fixture)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL + "/", APIKey: "k1"}
	rep, err := c.Report(context.Background(), strings.NewReader("shipment_id\nA\n"), Options{
		Organisation: "Acme & Co", PeriodFrom: "2026-01-01", PeriodTo: "2026-03-31",
		Scope3Category: 9, DeclareISO: true, NoRoadLegs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/v1/report" || got.Method != "POST" {
		t.Errorf("request %s %s", got.Method, got.URL.Path)
	}
	q := got.URL.Query()
	for k, v := range map[string]string{"results": "true", "organisation": "Acme & Co", "period_from": "2026-01-01",
		"period_to": "2026-03-31", "scope3_category": "9", "declare_iso": "true", "add_road_legs": "false"} {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Has("title") {
		t.Error("empty title sent")
	}
	if got.Header.Get("Authorization") != "Bearer k1" || got.Header.Get("Content-Type") != "text/csv" {
		t.Errorf("headers %v", got.Header)
	}
	if body != "shipment_id\nA\n" {
		t.Errorf("body %q", body)
	}
	if rep.Coverage.Shipments != 4 || len(rep.Results) != 4 || rep.Results[0].Result == nil {
		t.Errorf("decoded %+v", rep.Coverage)
	}
}

func TestReportErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"error":"no key"}`, "get an API key"},
		{429, `{"error":"100000 shipments a day"}`, "quota reached: 100000"},
		{503, `{"error":"busy"}`, "try again in 5 s"},
		{400, `{"error":"line 2: weight_kg"}`, "HTTP 400: line 2: weight_kg"},
		{502, `<html>bad gateway</html>`, "HTTP 502: <html>bad gateway</html>"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.body))
		}))
		_, err := (&Client{BaseURL: srv.URL}).Report(context.Background(), strings.NewReader(""), Options{})
		srv.Close()
		var e *Error
		if !errors.As(err, &e) || e.Status != tc.status || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%d: %v", tc.status, err)
		}
	}
}

func TestDecodeRefusesOtherJSON(t *testing.T) {
	if _, err := Decode([]byte(`{"wtw_kg": 5}`)); err == nil {
		t.Error("a calculate result was taken as a report")
	}
	if _, err := Decode([]byte(`not json`)); err == nil {
		t.Error("not JSON")
	}
}

// With SOOTCASE_TEST_URL set (CI starts a local Sootcase), check the real API.
func TestAgainstSootcase(t *testing.T) {
	url := os.Getenv("SOOTCASE_TEST_URL")
	if url == "" {
		t.Skip("SOOTCASE_TEST_URL not set")
	}
	csv, _ := os.ReadFile("../../testdata/shipments.csv")
	rep, err := (&Client{BaseURL: url}).Report(context.Background(), strings.NewReader(string(csv)),
		Options{Organisation: "Acme", Scope3Category: 4})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Coverage.Shipments != 4 || rep.Coverage.Failed != 1 || rep.GHG.Scope3Category != 4 {
		t.Errorf("coverage %+v, ghg %+v", rep.Coverage, rep.GHG)
	}
	// A1: 12 t x 332 km x 125 g (GLEC European HGV) = 498 kg on road.
	for _, it := range rep.Results {
		if it.ShipmentID == "A1" && (it.Result == nil || it.Result.Legs[0].DistanceKm != 332) {
			t.Errorf("A1: %+v", it)
		}
	}
	var road float64
	for _, m := range rep.Modes {
		if m.Mode == "road" {
			road = m.WTWKg
		}
	}
	if road < 497.99 || road > 498.01 {
		t.Errorf("road WTW = %v, want 498", road)
	}
}
