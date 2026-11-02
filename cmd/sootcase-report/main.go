// Command sootcase-report turns a shipments CSV into an ISO 14083-style
// freight emissions report as a PDF, calculated by a Sootcase server.
//
//	sootcase-report -org "Acme" -from 2026-01-01 -to 2026-03-31 -o q1.pdf shipments.csv
//	sootcase-report -json q1.json -o q1.pdf          # from a saved /v1/report answer
//	sootcase-report serve -addr :8090                # upload page and POST /report
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/majidkabir/sootcase-report/internal/render"
	"github.com/majidkabir/sootcase-report/internal/sootcase"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if err := serveMain(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "sootcase-report:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sootcase-report:", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("sootcase-report", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `Usage:
  sootcase-report [flags] shipments.csv     calculate with Sootcase and write a PDF
  sootcase-report -json report.json [flags] render a saved /v1/report?results=true answer
  sootcase-report serve [flags]             run the upload page (see serve -h)

The CSV has Sootcase's batch columns (one row per leg or hub stop), "-" reads stdin.

Flags:
`)
		fs.PrintDefaults()
	}
	var (
		url      = fs.String("url", env("SOOTCASE_URL", sootcase.DefaultURL), "Sootcase server (env SOOTCASE_URL)")
		key      = fs.String("key", os.Getenv("SOOTCASE_API_KEY"), "API key from sootcase.org (env SOOTCASE_API_KEY)")
		out      = fs.String("o", "report.pdf", `output PDF, "-" for stdout`)
		jsonIn   = fs.String("json", "", "render this saved report JSON instead of calling the API")
		saveJSON = fs.String("save-json", "", "also save the API's answer to this file")
		title    = fs.String("title", "", "report title")
		org      = fs.String("org", "", "organisation")
		from     = fs.String("from", "", "period start, YYYY-MM-DD")
		to       = fs.String("to", "", "period end, YYYY-MM-DD")
		ref      = fs.String("reference", "", "where supporting information can be found")
		scope3   = fs.Int("scope3", 0, "GHG Protocol scope 3 category of the transport: 4 (bought) or 9 (sold products)")
		iso      = fs.Bool("declare-iso", false, "add the ISO 14083:2023 conformity statement (only you can vouch for it)")
		roadLegs = fs.Bool("road-legs", true, "let Sootcase add missing road legs (to ports, terminals, airports)")
		timeout  = fs.Duration("timeout", 10*time.Minute, "how long to wait for the server")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *scope3 != 0 && *scope3 != 4 && *scope3 != 9 {
		return errors.New("-scope3 is 4 or 9")
	}

	var rep *sootcase.Report
	switch {
	case *jsonIn != "":
		if fs.NArg() > 0 {
			return errors.New("give either -json or a CSV, not both")
		}
		b, err := os.ReadFile(*jsonIn)
		if err != nil {
			return err
		}
		if rep, err = sootcase.Decode(b); err != nil {
			return err
		}
	case fs.NArg() == 1:
		var in io.Reader = stdin
		if p := fs.Arg(0); p != "-" {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			in = f
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		c := &sootcase.Client{BaseURL: *url, APIKey: *key}
		var raw []byte
		var err error
		rep, raw, err = c.ReportRaw(ctx, in, sootcase.Options{
			Title: *title, Organisation: *org, PeriodFrom: *from, PeriodTo: *to, Reference: *ref,
			DeclareISO: *iso, Scope3Category: *scope3, NoRoadLegs: !*roadLegs,
		})
		if err != nil {
			return err
		}
		if *saveJSON != "" {
			if err := os.WriteFile(*saveJSON, raw, 0o644); err != nil {
				return err
			}
		}
	default:
		fs.Usage()
		return errors.New("give one shipments CSV, or -json")
	}

	b := render.PDF(rep)
	if *out == "-" {
		_, err := stdout.Write(b)
		return err
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return err
	}
	c := rep.Coverage
	msg := fmt.Sprintf("%s: %d of %d shipments, %.3f t CO2e WTW", *out, c.Calculated, c.Shipments, rep.Total.WTWKg/1000)
	if c.Failed > 0 {
		msg += fmt.Sprintf(", %d failed (listed in the report)", c.Failed)
	}
	fmt.Fprintln(os.Stderr, strings.TrimSpace(msg))
	return nil
}
