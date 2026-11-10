package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/majidkabir/sootcase-report/internal/render"
	"github.com/majidkabir/sootcase-report/internal/sootcase"
)

const maxUpload = 10 << 20 // the Sootcase API's body limit

func serveMain(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", env("ADDR", ":8090"), "listen address (env ADDR)")
	url := fs.String("url", env("SOOTCASE_URL", sootcase.DefaultURL), "Sootcase server (env SOOTCASE_URL)")
	key := fs.String("key", os.Getenv("SOOTCASE_API_KEY"), "API key used when a request brings none (env SOOTCASE_API_KEY)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           newServer(*url, *key),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // a 10 MiB upload on a slow line
		IdleTimeout:       2 * time.Minute,
		WriteTimeout:      11 * time.Minute,
	}
	log.Printf("sootcase-report listening on %s, calculating with %s", *addr, *url)
	return srv.ListenAndServe()
}

// newServer serves the upload page at /, POST /report and /healthz.
//
// POST /report takes a multipart form (file: the shipments CSV; organisation,
// title, period_from, period_to, reference, scope3_category, declare_iso,
// add_road_legs, api_key) or a text/csv body with the same fields in the
// query, and answers with the PDF. The caller's "Authorization: Bearer" header
// or api_key field is passed on to Sootcase; without either, the server's key.
func newServer(baseURL, key string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page.Execute(w, nil)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("POST /report", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<20)
		field, csv, err := requestInput(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		defer csv.Close()

		o := sootcase.Options{
			Title:        field("title"),
			Organisation: field("organisation"),
			PeriodFrom:   field("period_from"),
			PeriodTo:     field("period_to"),
			Reference:    field("reference"),
			DeclareISO:   field("declare_iso") == "true" || field("declare_iso") == "on",
			NoRoadLegs:   field("add_road_legs") == "false",
		}
		if v := field("scope3_category"); v != "" {
			o.Scope3Category, _ = strconv.Atoi(v)
		}
		k := key
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
			k = strings.TrimPrefix(a, "Bearer ")
		} else if v := field("api_key"); v != "" {
			k = v
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		c := &sootcase.Client{BaseURL: baseURL, APIKey: k}
		rep, err := c.Report(ctx, csv, o)
		if err != nil {
			var e *sootcase.Error
			if errors.As(err, &e) {
				if e.RetryAfter != "" {
					w.Header().Set("Retry-After", e.RetryAfter)
				}
				httpError(w, e.Status, err.Error())
				return
			}
			httpError(w, http.StatusBadGateway, err.Error())
			return
		}
		b := render.PDF(rep)
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="`+fileName(rep)+`"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	return securityHeaders(mux)
}

type readCloser interface {
	Read([]byte) (int, error)
	Close() error
}

// requestInput returns the form fields and the CSV of a /report request.
func requestInput(r *http.Request) (func(string) string, readCloser, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/csv") {
		q := r.URL.Query()
		return q.Get, r.Body, nil
	}
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		return nil, nil, fmt.Errorf("send a multipart form with the CSV as \"file\", or a text/csv body: %v", err)
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		return nil, nil, errors.New("no shipments CSV in the \"file\" field")
	}
	return func(k string) string { return strings.TrimSpace(r.FormValue(k)) }, f, nil
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func fileName(r *sootcase.Report) string {
	parts := []string{"sootcase-report"}
	if r.Organisation != "" {
		parts = append(parts, r.Organisation)
	}
	if r.Period.From != "" {
		parts = append(parts, r.Period.From)
	}
	if r.Period.To != "" {
		parts = append(parts, r.Period.To)
	}
	s := unsafeName.ReplaceAllString(strings.Join(parts, "_"), "-")
	if len(s) > 120 {
		s = s[:120]
	}
	return s + ".pdf"
}

func httpError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, "{\"error\":%q}\n", msg)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		h.ServeHTTP(w, r)
	})
}

var page = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sootcase report: freight emissions as a PDF</title>
<style>
  :root { color-scheme: light dark; --accent: #1F5E8C; }
  body { font: 16px/1.5 system-ui, sans-serif; max-width: 40rem; margin: 2rem auto; padding: 0 1rem; }
  h1 { font-size: 1.5rem; margin-bottom: .25rem; }
  p.lead { margin-top: 0; opacity: .8; }
  label { display: block; margin-top: 1rem; font-weight: 600; }
  label small { font-weight: 400; opacity: .75; }
  input, select { font: inherit; width: 100%; box-sizing: border-box; padding: .45rem; margin-top: .25rem; }
  input[type=checkbox] { width: auto; margin-right: .4rem; }
  .row { display: flex; gap: 1rem; } .row > div { flex: 1; }
  .check { font-weight: 400; }
  button { font: inherit; margin-top: 1.5rem; padding: .6rem 1.2rem; background: var(--accent); color: #fff; border: 0; border-radius: 4px; cursor: pointer; }
  footer { margin-top: 2rem; font-size: .85rem; opacity: .75; }
  a { color: var(--accent); }
</style>
</head>
<body>
<h1>Freight emissions report as a PDF</h1>
<p class="lead">Upload a shipments CSV and get an ISO 14083-style report: totals split into operational and energy provision emissions, per mode, hubs, energy carriers, GHG Protocol scopes, data quality and notes. Calculated by <a href="https://sootcase.org">Sootcase</a> following the GLEC Framework v3.3.</p>
<form method="post" action="/report" enctype="multipart/form-data">
  <label>Shipments CSV <small>(Sootcase's <a href="https://github.com/majidkabir/sootcase#inputs">batch columns</a>, up to 10,000 shipments)</small>
    <input type="file" name="file" accept=".csv,text/csv" required></label>
  <label>Organisation <input name="organisation" maxlength="1000"></label>
  <label>Title <small>(optional)</small> <input name="title" maxlength="1000" placeholder="Freight GHG emissions report"></label>
  <div class="row">
    <div><label>Period from <input type="date" name="period_from"></label></div>
    <div><label>Period to <input type="date" name="period_to"></label></div>
  </div>
  <label>GHG Protocol scope 3 category
    <select name="scope3_category">
      <option value="">Not stated</option>
      <option value="4">4: transport you pay for</option>
      <option value="9">9: sold products, transport you don't pay for</option>
    </select></label>
  <label>Where supporting information is <small>(optional)</small> <input name="reference" maxlength="1000"></label>
  <label>API key <small>(from sootcase.org; sent to Sootcase only)</small> <input name="api_key" autocomplete="off"></label>
  <label class="check"><input type="checkbox" name="declare_iso" value="true">Add the ISO 14083:2023 conformity statement (only you can vouch for your data and boundaries)</label>
  <button type="submit">Make the PDF</button>
</form>
<footer>Source: <a href="https://github.com/majidkabir/sootcase-report">github.com/majidkabir/sootcase-report</a>. Results are calculations for your own reporting; Sootcase is not certified against ISO 14083.</footer>
</body>
</html>
`))
