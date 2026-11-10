package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSootcase answers /v1/report with the fixture and records the request.
func fakeSootcase(t *testing.T) (*httptest.Server, *http.Request) {
	fixture, err := os.ReadFile("../../testdata/report.json")
	if err != nil {
		t.Fatal(err)
	}
	got := &http.Request{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = *r.Clone(r.Context())
		io.Copy(io.Discard, r.Body)
		w.Write(fixture)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestRunWritesPDF(t *testing.T) {
	srv, got := fakeSootcase(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "q1.pdf")
	saved := filepath.Join(dir, "q1.json")
	err := run([]string{"-url", srv.URL, "-key", "abc", "-org", "Acme", "-from", "2026-01-01", "-scope3", "4",
		"-o", out, "-save-json", saved, "../../testdata/shipments.csv"}, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		t.Fatal("no PDF")
	}
	if got.URL.Query().Get("organisation") != "Acme" || got.Header.Get("Authorization") != "Bearer abc" {
		t.Errorf("request %v %v", got.URL, got.Header)
	}
	j, _ := os.ReadFile(saved)
	if !json.Valid(j) {
		t.Error("saved JSON")
	}

	// The saved JSON renders the same report offline.
	var stdout bytes.Buffer
	if err := run([]string{"-json", saved, "-o", "-"}, nil, &stdout); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(stdout.Bytes(), []byte("%PDF-")) {
		t.Error("stdout PDF")
	}
}

func TestRunArguments(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"-scope3", "5", "x.csv"},
		{"-json", "x.json", "x.csv"},
		{"a.csv", "b.csv"},
	} {
		if err := run(args, nil, io.Discard); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}

func TestServeForm(t *testing.T) {
	api, got := fakeSootcase(t)
	h := newServer(api.URL, "server-key")

	// The upload page.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `action="/report"`) {
		t.Fatalf("page: %d", rec.Code)
	}

	// A multipart upload with the user's own key.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "shipments.csv")
	csv, _ := os.ReadFile("../../testdata/shipments.csv")
	fw.Write(csv)
	mw.WriteField("organisation", "Acme A/S")
	mw.WriteField("period_from", "2026-01-01")
	mw.WriteField("scope3_category", "9")
	mw.WriteField("declare_iso", "true")
	mw.WriteField("api_key", "user-key")
	mw.Close()
	req := httptest.NewRequest("POST", "/report", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "sootcase-report_Acme-Logistics-A-S_2026-01-01_2026-03-31.pdf") {
		t.Errorf("file name: %s", cd)
	}
	q := got.URL.Query()
	if q.Get("organisation") != "Acme A/S" || q.Get("scope3_category") != "9" || q.Get("declare_iso") != "true" {
		t.Errorf("query %v", q)
	}
	if got.Header.Get("Authorization") != "Bearer user-key" {
		t.Errorf("key: %s", got.Header.Get("Authorization"))
	}

	// A CSV body with a bearer header; without either, the server's key.
	req = httptest.NewRequest("POST", "/report?organisation=X", bytes.NewReader(csv))
	req.Header.Set("Content-Type", "text/csv")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || got.Header.Get("Authorization") != "Bearer server-key" {
		t.Errorf("csv body: %d, key %s", rec.Code, got.Header.Get("Authorization"))
	}

	// No file.
	req = httptest.NewRequest("POST", "/report", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("no file: %d", rec.Code)
	}
}

func TestServePassesAPIErrors(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(503)
		w.Write([]byte(`{"error":"busy"}`))
	}))
	defer api.Close()
	req := httptest.NewRequest("POST", "/report", strings.NewReader("shipment_id\n"))
	req.Header.Set("Content-Type", "text/csv")
	rec := httptest.NewRecorder()
	newServer(api.URL, "").ServeHTTP(rec, req)
	if rec.Code != 503 || rec.Header().Get("Retry-After") != "7" {
		t.Errorf("%d %v", rec.Code, rec.Header())
	}
}

func TestServeTooBig(t *testing.T) {
	api, _ := fakeSootcase(t)
	h := newServer(api.URL, "")
	big := bytes.Repeat([]byte("a"), maxUpload+2<<20)

	req := httptest.NewRequest("POST", "/report", bytes.NewReader(big))
	req.Header.Set("Content-Type", "text/csv")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("csv body: %d %s", rec.Code, rec.Body.String())
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "big.csv")
	fw.Write(big)
	mw.Close()
	req = httptest.NewRequest("POST", "/report", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("multipart: %d %s", rec.Code, rec.Body.String())
	}
}
