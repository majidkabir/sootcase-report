package render

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/majidkabir/sootcase-report/internal/sootcase"
)

func load(t *testing.T) *sootcase.Report {
	t.Helper()
	b, err := os.ReadFile("../../testdata/report.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := sootcase.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// text renders the report and extracts its text with poppler's pdftotext.
func text(t *testing.T, r *sootcase.Report) (string, []byte) {
	t.Helper()
	b := PDF(r)
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		t.Fatal("not a PDF")
	}
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	f := filepath.Join(t.TempDir(), "r.pdf")
	os.WriteFile(f, b, 0o644)
	out, err := exec.Command("pdftotext", "-layout", f, "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out), b
}

func TestReportContents(t *testing.T) {
	r := load(t)
	txt, _ := text(t, r)
	// The numbers ISO 14083 asks a report to state, as the API gave them.
	for _, want := range []string{
		"Acme Logistics A/S",
		"1 Jan 2026 to 31 Mar 2026",
		"Scope 3, category 4",
		"4.74",         // total t CO2e WTW
		"3.27", "0.76", // TTW, WTT in t
		"19,683",           // tonne-km
		"204.7",            // g/tkm
		"3 of 4",           // shipments
		"3,361", "1,413.0", // air WTW kg, intensity
		"Warehouse", "35,500.0",
		"Great circle",
		"Sources of default values",
		"GLEC Framework v3.3 (Smart Freight Centre, 2026), Section 3 Module 2, Table 1, air",
		"Largest shipments (3 of 3)",
		"Copenhagen Kastrup Airport (CPH) ->", "Frankfurt Main Airport (FRA) (air)",
		"weight_kg: \"abc\" is not a number",
		"Notes on omissions and deviations",
		"not certified against ISO 14083",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(txt, "Calculated with Sootcase (sootcase.org)"); n < 2 {
		t.Errorf("footers: %d", n)
	}
	if !regexp.MustCompile(`Page 1 of (\d+)`).MatchString(txt) {
		t.Error("no page numbers")
	}
}

func TestLongTablesBreakAcrossPages(t *testing.T) {
	r := load(t)
	r.Coverage.Failures = nil
	for i := 0; i < 20; i++ {
		r.Coverage.Failures = append(r.Coverage.Failures, sootcase.Failure{ShipmentID: fmt.Sprintf("F%03d", i), Error: strings.Repeat("a long reason ", 12)})
	}
	r.Coverage.Failed = 300
	r.Data.DefaultSources = nil
	for i := 0; i < 80; i++ {
		r.Data.DefaultSources = append(r.Data.DefaultSources, sootcase.Source{Source: fmt.Sprintf("Source number %03d, with a long description that wraps onto a second line in the table cell", i), WTWKg: 1, Share: 0.0125})
	}
	txt, _ := text(t, r)
	for i := 0; i < 80; i++ {
		if !strings.Contains(txt, fmt.Sprintf("Source number %03d", i)) {
			t.Fatalf("source %d lost", i)
		}
	}
	for i := 0; i < 20; i++ {
		if !strings.Contains(txt, fmt.Sprintf("F%03d", i)) {
			t.Fatalf("failure %d lost", i)
		}
	}
	if !strings.Contains(txt, "Failed shipments (20 of 300)") {
		t.Error("failure count heading")
	}
	m := regexp.MustCompile(`Page 1 of (\d+)`).FindStringSubmatch(txt)
	if m == nil {
		t.Fatal("no page numbers")
	}
	if n, _ := strconv.Atoi(m[1]); n < 5 {
		t.Errorf("expected several pages, got %v", m)
	}
	// The table header is repeated on the next page.
	if h := regexp.MustCompile(`(?m)^\s*Source\s+WTW kg CO2e\s+Share\s*$`).FindAllString(txt, -1); len(h) < 2 {
		t.Errorf("header not repeated: %d", len(h))
	}
}

func TestLongOrganisationFitsFooter(t *testing.T) {
	r := load(t)
	r.Organisation = strings.Repeat("Very Long Organisation Name ", 10)
	txt, _ := text(t, r)
	for _, line := range strings.Split(txt, "\n") {
		if strings.Contains(line, "Page 1 of") && !strings.Contains(line, "…") {
			t.Errorf("footer not shortened: %q", line)
		}
	}
}

func TestVeryLongTextIsClipped(t *testing.T) {
	r := load(t)
	r.Title = strings.Repeat("Quarterly freight emissions ", 40)
	r.Organisation = strings.Repeat("Organisation ", 80)
	r.Coverage.Failures = []sootcase.Failure{{ShipmentID: "X", Error: strings.Repeat("very long error ", 600)}}
	txt, b := text(t, r)
	if strings.Count(txt, "…") < 3 {
		t.Errorf("expected title, organisation and the long error to be clipped")
	}
	// The cover still fits on page 1 with the key figures.
	f := filepath.Join(t.TempDir(), "p1.pdf")
	os.WriteFile(f, b, 0o644)
	p1, _ := exec.Command("pdftotext", "-f", "1", "-l", "1", f, "-").Output()
	if !strings.Contains(string(p1), "Key figures") {
		t.Error("cover pushed the key figures off page 1")
	}
}

// Every kind of input: electricity, measured fuel, own value, load model,
// containers with a per-container hub, a temperature-controlled warehouse.
func TestRichReport(t *testing.T) {
	b, err := os.ReadFile("../../testdata/rich.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := sootcase.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	txt, _ := text(t, r)
	for _, want := range []string{
		"Electricity, DNK grid", "glec/fuel/eu/diesel/l", "Not stated (published intensity",
		"Models and their parameters", "load_factor_source", "Intermodal terminal", "Warehouse",
		"Primary data", "Actual", "6 of 6",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q", want)
		}
	}
	// No period given: the footer doesn't say "not stated".
	for _, line := range strings.Split(txt, "\n") {
		if strings.Contains(line, "Page 1 of") && strings.Contains(line, "not stated") {
			t.Errorf("footer: %q", line)
		}
	}
}

func TestEmptyReport(t *testing.T) {
	r := &sootcase.Report{Level: "transport_or_hub_services"}
	b := PDF(r)
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		t.Fatal("not a PDF")
	}
}

func TestFormatting(t *testing.T) {
	cases := map[string]string{
		number(1234567.891, 1): "1,234,567.9",
		number(-1234, 0):       "-1,234",
		number(-0.0001, 1):     "0.0",
		number(999, 0):         "999",
		kg(0.123):              "0.12",
		kg(12.34):              "12.3",
		kg(12345.6):            "12,346",
		tonnes(4739.79):        "4.74",
		tonnes(49.3):           "0.049",
		tonnes(1234567):        "1,235",
		pct(0.0004):            "<0.1%",
		pct(0.026):             "2.6%",
		pct(0.71):              "71%",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	for in, want := range map[string]string{
		"unspecified":           "Not stated (published intensity or load model)",
		"electricity/DK":        "Electricity, DK grid",
		"glec/fuel/eu/diesel/l": "glec/fuel/eu/diesel/l",
	} {
		if got := carrierName(in); got != want {
			t.Errorf("carrierName(%q) = %q", in, got)
		}
	}
	if s := scope3Label(4); s != "Scope 3, category 4 (upstream transportation and distribution)" {
		t.Error(s)
	}
	if s := scope3Label(7); s != "Scope 3, category 7" {
		t.Error(s)
	}
	if p := period(sootcase.Period{From: "2026-01-01"}); p != "from 1 Jan 2026" {
		t.Error(p)
	}
}
