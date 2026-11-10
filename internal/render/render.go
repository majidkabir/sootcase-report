// Package render lays out a Sootcase report as a PDF.
package render

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/majidkabir/sootcase-report/internal/pdf"
	"github.com/majidkabir/sootcase-report/internal/sootcase"
)

var (
	ink    = pdf.Hex("#1d2329")
	grey   = pdf.Hex("#5f6b76")
	faint  = pdf.Hex("#d5dbe0")
	zebra  = pdf.Hex("#f3f5f7")
	accent = pdf.Hex("#1F5E8C") // Sootcase blue: tank-to-wheel
	light  = pdf.Hex("#8DB4D3") // well-to-tank
	hubCol = pdf.Hex("#C98A2B") // hubs
	white  = pdf.Color{R: 1, G: 1, B: 1}
	dqCols = []pdf.Color{pdf.Hex("#2E7D5B"), pdf.Hex("#8DB4D3"), pdf.Hex("#C9CFD5")} // primary, modelled, default
)

const (
	margin   = 48.0
	top      = 56.0
	bottom   = pdf.A4Height - 58
	contentW = pdf.A4Width - 2*margin
)

// PDF renders the report.
func PDF(r *sootcase.Report) []byte {
	d := pdf.New()
	d.Title = r.Title
	d.Author = r.Organisation
	d.Subject = "Freight GHG emissions (GLEC Framework / ISO 14083), calculated with Sootcase"
	d.Created = r.Generated
	l := &layout{d: d}
	l.newPage()

	cover(l, r)
	keyFigures(l, r)
	modes(l, r)
	hubs(l, r)
	energy(l, r)
	ghg(l, r)
	distances(l, r)
	dataQuality(l, r)
	largest(l, r)
	coverage(l, r)
	notes(l, r)
	method(l, r)

	footers(l, r)
	return d.Bytes()
}

// ---- layout ----

type layout struct {
	d *pdf.Doc
	y float64
}

func (l *layout) newPage() {
	l.d.AddPage()
	l.y = top
}

// need starts a new page unless h more points fit on this one.
func (l *layout) need(h float64) {
	if l.y+h > bottom {
		l.newPage()
	}
}

func (l *layout) heading(s string) {
	l.need(70) // keep a heading with what follows it
	l.y += 18
	l.d.Text(margin, l.y, pdf.Bold, 13, accent, s)
	l.y += 6
	l.d.Line(margin, l.y, margin+contentW, l.y, 0.6, faint)
	l.y += 14
}

func (l *layout) sub(s string) {
	l.need(40)
	l.y += 6
	l.d.Text(margin, l.y, pdf.Bold, 9.5, ink, s)
	l.y += 12
}

// para writes wrapped text at x with width w.
func (l *layout) para(s string, f pdf.Font, size float64, c pdf.Color) {
	lh := size * 1.35
	for _, line := range pdf.Wrap(f, size, s, contentW) {
		l.need(lh)
		l.y += size
		l.d.Text(margin, l.y, f, size, c, line)
		l.y += lh - size
	}
}

func (l *layout) bullets(items []string, size float64) {
	lh := size * 1.35
	for _, it := range items {
		lines := pdf.Wrap(pdf.Regular, size, it, contentW-12)
		l.need(lh * math.Min(float64(len(lines)), 3))
		for i, line := range lines {
			l.need(lh)
			l.y += size
			if i == 0 {
				l.d.Text(margin+2, l.y, pdf.Regular, size, accent, "•")
			}
			l.d.Text(margin+12, l.y, pdf.Regular, size, ink, line)
			l.y += lh - size
		}
		l.y += 3
	}
}

// maxCellLines keeps a table row shorter than a page.
const maxCellLines = 40

// clip keeps the first n lines, marking a cut with "…".
func clip(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	lines = append([]string(nil), lines[:n]...)
	lines[n-1] += " …"
	return lines
}

type col struct {
	title string
	w     float64 // share of the content width
	right bool
}

// table writes rows with wrapped cells, repeating the header after a page
// break. A row starting with "\x00" is a bold total row.
func (l *layout) table(cols []col, rows [][]string) {
	const size, pad = 8.0, 4.0
	lh := size * 1.3
	widths := make([]float64, len(cols))
	var sum float64
	for _, c := range cols {
		sum += c.w
	}
	for i, c := range cols {
		widths[i] = c.w / sum * contentW
	}
	titles := make([][]string, len(cols))
	hl := 1
	for i, c := range cols {
		titles[i] = pdf.Wrap(pdf.Bold, size, c.title, widths[i]-2*pad)
		hl = max(hl, len(titles[i]))
	}
	header := func() {
		h := float64(hl)*lh + 2*pad
		l.d.Rect(margin, l.y, contentW, h, accent)
		x := margin
		for i, c := range cols {
			for j, line := range titles[i] {
				y := l.y + pad + size + float64(j)*lh
				if c.right {
					l.d.TextRight(x+widths[i]-pad, y, pdf.Bold, size, white, line)
				} else {
					l.d.Text(x+pad, y, pdf.Bold, size, white, line)
				}
			}
			x += widths[i]
		}
		l.y += h
	}
	showHeader := false
	for _, c := range cols {
		showHeader = showHeader || c.title != ""
	}
	if !showHeader {
		header = func() { l.d.Line(margin, l.y, margin+contentW, l.y, 0.6, faint) }
	}
	l.need(2 * (lh + 2*pad))
	header()
	for n, row := range rows {
		bold := len(row) > 0 && strings.HasPrefix(row[0], "\x00")
		f := pdf.Regular
		if bold {
			f = pdf.Bold
			row = append([]string{strings.TrimPrefix(row[0], "\x00")}, row[1:]...)
		}
		cells := make([][]string, len(cols))
		lines := 1
		for i := range cols {
			v := ""
			if i < len(row) {
				v = row[i]
			}
			cells[i] = clip(pdf.Wrap(f, size, v, widths[i]-2*pad), maxCellLines)
			lines = max(lines, len(cells[i]))
		}
		h := float64(lines)*lh + 2*pad
		if l.y+h > bottom {
			l.newPage()
			header()
		}
		if bold {
			l.d.Line(margin, l.y, margin+contentW, l.y, 0.8, ink)
		} else if n%2 == 1 {
			l.d.Rect(margin, l.y, contentW, h, zebra)
		}
		x := margin
		for i, c := range cols {
			for j, line := range cells[i] {
				y := l.y + pad + size + float64(j)*lh
				if c.right {
					l.d.TextRight(x+widths[i]-pad, y, f, size, ink, line)
				} else {
					l.d.Text(x+pad, y, f, size, ink, line)
				}
			}
			x += widths[i]
		}
		l.y += h
	}
	l.y += 8
}

// legend draws colored squares with labels on one line.
func (l *layout) legend(items []string, colors []pdf.Color) {
	l.need(16)
	x := margin
	for i, s := range items {
		l.d.Rect(x, l.y+1, 8, 8, colors[i])
		l.d.Text(x+12, l.y+8, pdf.Regular, 8, grey, s)
		x += 12 + pdf.Width(pdf.Regular, 8, s) + 16
	}
	l.y += 16
}

// ---- sections ----

func cover(l *layout, r *sootcase.Report) {
	d := l.d
	d.Rect(0, 0, pdf.A4Width, 74, accent)
	d.Text(margin, 34, pdf.Bold, 20, white, "Sootcase")
	d.Text(margin, 54, pdf.Regular, 10, white, "Freight emissions following the GLEC Framework and ISO 14083")
	l.y = 112

	title := r.Title
	if title == "" {
		title = "Freight GHG emissions report"
	}
	for _, line := range clip(pdf.Wrap(pdf.Bold, 22, title, contentW), 4) {
		d.Text(margin, l.y, pdf.Bold, 22, ink, line)
		l.y += 28
	}
	l.y += 2

	meta := [][2]string{}
	if r.Organisation != "" {
		meta = append(meta, [2]string{"Organisation", r.Organisation})
	}
	meta = append(meta, [2]string{"Reporting period", period(r.Period)})
	meta = append(meta, [2]string{"Level", level(r.Level)})
	if r.GHG.Scope3Category != 0 {
		meta = append(meta, [2]string{"GHG Protocol", scope3Label(r.GHG.Scope3Category)})
	}
	meta = append(meta, [2]string{"Emission factors", strings.Join(r.Datasets, ", ")})
	if !r.Generated.IsZero() {
		meta = append(meta, [2]string{"Calculated", r.Generated.UTC().Format("2 January 2006, 15:04 UTC")})
	}
	for _, m := range meta {
		lines := clip(pdf.Wrap(pdf.Regular, 9.5, m[1], contentW-120), 6)
		d.Text(margin, l.y, pdf.Bold, 9.5, grey, m[0])
		for i, line := range lines {
			d.Text(margin+120, l.y+float64(i)*13, pdf.Regular, 9.5, ink, line)
		}
		l.y += float64(len(lines))*13 + 3
	}
	l.y += 6
	if r.Statement != "" {
		l.para(r.Statement, pdf.Regular, 9, grey)
	}
}

func keyFigures(l *layout, r *sootcase.Report) {
	l.heading("Key figures")
	t, c := r.Total, r.Coverage
	boxes := [][3]string{
		{"Total emissions (WTW)", tonnes(t.WTWKg), "t CO2e"},
		{"Transport, operational (TTW)", tonnes(t.TTWKg), "t CO2e"},
		{"Transport, energy provision (WTT)", tonnes(t.WTTKg), "t CO2e"},
		{"Hubs (WTW)", tonnes(t.HubWTWKg), "t CO2e"},
		{"Transport activity", number(t.TonneKm, 0), "tonne-km"},
		{"Transport intensity (WTW)", number(t.IntensityG, 1), "g CO2e / tonne-km"},
		{"Shipments calculated", fmt.Sprintf("%s of %s", number(float64(c.Calculated), 0), number(float64(c.Shipments), 0)), plural(c.Failed, "failed")},
		{"Cargo", number(c.WeightT, 1), "tonnes"},
	}
	const gap, h = 10.0, 60.0
	w := (contentW - 3*gap) / 4
	for row := 0; row < 2; row++ {
		l.need(h + gap)
		for i := 0; i < 4; i++ {
			b := boxes[row*4+i]
			x := margin + float64(i)*(w+gap)
			l.d.Rect(x, l.y, w, h, zebra)
			l.d.Rect(x, l.y, 3, h, accent)
			for j, line := range pdf.Wrap(pdf.Regular, 7.5, b[0], w-16) {
				if j < 2 {
					l.d.Text(x+10, l.y+13+float64(j)*9, pdf.Regular, 7.5, grey, line)
				}
			}
			size := 15.0
			for size > 9 && pdf.Width(pdf.Bold, size, b[1]) > w-16 {
				size--
			}
			l.d.Text(x+10, l.y+43, pdf.Bold, size, ink, b[1])
			l.d.Text(x+10, l.y+54, pdf.Regular, 7.5, grey, b[2])
		}
		l.y += h + gap
	}
	if t.BiogenicCO2Kg > 0 {
		l.para(fmt.Sprintf("Biogenic CO2 from biofuels: %s kg, reported separately and not included in the totals (GLEC Framework v3.3, p. 64).",
			number(t.BiogenicCO2Kg, 1)), pdf.Regular, 8.5, grey)
	}
}

func modes(l *layout, r *sootcase.Report) {
	l.heading("Emissions by mode of transport")
	type bar struct {
		label         string
		ttw, wtt, all float64
		hub           bool
	}
	var bars []bar
	for _, m := range r.Modes {
		bars = append(bars, bar{label: modeName(m.Mode), ttw: m.TTWKg, wtt: m.WTTKg, all: m.WTWKg})
	}
	if r.Total.HubWTWKg > 0 {
		bars = append(bars, bar{label: "Hubs", all: r.Total.HubWTWKg, hub: true})
	}
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].all > bars[j].all })
	var top float64
	for _, b := range bars {
		top = math.Max(top, b.all)
	}
	if len(bars) > 0 && top > 0 {
		const labelW, valueW, bh, gap = 92.0, 96.0, 14.0, 6.0
		maxW := contentW - labelW - valueW
		for _, b := range bars {
			l.need(bh + gap)
			l.d.Text(margin, l.y+bh-3.5, pdf.Regular, 9, ink, b.label)
			x := margin + labelW
			if b.hub {
				l.d.Rect(x, l.y, b.all/top*maxW, bh, hubCol)
			} else {
				w1 := b.ttw / top * maxW
				l.d.Rect(x, l.y, w1, bh, accent)
				l.d.Rect(x+w1, l.y, b.wtt/top*maxW, bh, light)
			}
			share := ""
			if r.Total.WTWKg > 0 {
				share = "  " + pct(b.all/r.Total.WTWKg)
			}
			l.d.TextRight(margin+contentW, l.y+bh-3.5, pdf.Regular, 9, ink, tonnes(b.all)+" t"+share)
			l.y += bh + gap
		}
		items, colors := []string{"Operational (tank-to-wheel)", "Energy provision (well-to-tank)"}, []pdf.Color{accent, light}
		if r.Total.HubWTWKg > 0 {
			items, colors = append(items, "Hubs (well-to-wheel)"), append(colors, hubCol)
		}
		l.legend(items, colors)
		l.y += 4
	}

	rows := [][]string{}
	for _, m := range r.Modes {
		rows = append(rows, []string{modeName(m.Mode), number(float64(m.Legs), 0), number(m.TonneKm, 0),
			kg(m.TTWKg), kg(m.WTTKg), kg(m.WTWKg), number(m.IntensityG, 1), share(m.WTWKg, r.Total.TransportWTWKg)})
	}
	t := r.Total
	rows = append(rows, []string{"\x00Transport", number(float64(r.Coverage.Legs), 0), number(t.TonneKm, 0),
		kg(t.TTWKg), kg(t.WTTKg), kg(t.TransportWTWKg), number(t.IntensityG, 1), share(t.TransportWTWKg, t.TransportWTWKg)})
	l.table([]col{{"Mode", 1.6, false}, {"Legs", 0.8, true}, {"Tonne-km", 1.3, true}, {"TTW kg CO2e", 1.3, true},
		{"WTT kg CO2e", 1.3, true}, {"WTW kg CO2e", 1.3, true}, {"g CO2e/tkm", 1.1, true}, {"Share of transport", 1.1, true}}, rows)
}

func hubs(l *layout, r *sootcase.Report) {
	if len(r.Hubs) == 0 {
		return
	}
	l.heading("Emissions at hubs")
	rows := [][]string{}
	for _, h := range r.Hubs {
		rows = append(rows, []string{label(h.HubType), number(float64(h.Stops), 0), number(h.Tonnes, 1), kg(h.WTWKg), number(h.IntensityGT, 1)})
	}
	rows = append(rows, []string{"\x00All hubs", number(float64(r.Coverage.HubStops), 0), "", kg(r.Total.HubWTWKg), ""})
	l.table([]col{{"Hub type", 2.4, false}, {"Stops", 0.8, true}, {"Tonnes handled", 1.2, true},
		{"WTW kg CO2e", 1.2, true}, {"g CO2e/t", 1.1, true}}, rows)
}

func energy(l *layout, r *sootcase.Report) {
	if len(r.Energy) == 0 {
		return
	}
	l.heading("Emissions by energy carrier")
	rows := [][]string{}
	for _, e := range r.Energy {
		rows = append(rows, []string{carrierName(e.Carrier), kg(e.TTWKg), kg(e.WTTKg), kg(e.WTWKg), pct(e.Share)})
	}
	l.table([]col{{"Energy carrier", 2.4, false}, {"TTW kg CO2e", 1.2, true}, {"WTT kg CO2e", 1.2, true},
		{"WTW kg CO2e", 1.2, true}, {"Share of transport", 1.2, true}}, rows)
	l.para("Fuels are named by the fuel factor used for measured fuel; electricity by the grid it was drawn from.", pdf.Regular, 8, grey)
}

// carrierName shows the API's carrier id: "unspecified", a fuel factor id
// such as "glec/fuel/eu/diesel/l", or "electricity/<country>".
func carrierName(c string) string {
	switch {
	case c == "unspecified":
		return "Not stated (published intensity or load model)"
	case strings.HasPrefix(c, "electricity/"):
		return "Electricity, " + strings.TrimPrefix(c, "electricity/") + " grid"
	}
	return c
}

func ghg(l *layout, r *sootcase.Report) {
	l.heading("GHG Protocol scopes")
	g := r.GHG
	shipper := "Scope 3 (transport bought)"
	if g.Scope3Category != 0 {
		shipper = scope3Label(g.Scope3Category)
	}
	l.sub("For the company buying the transport")
	l.table([]col{{"Scope", 4, false}, {"kg CO2e", 1.2, true}}, [][]string{{shipper, kg(g.Scope3ShipperKg)}})
	l.sub("For the operator of the vehicles and hubs")
	l.table([]col{{"Scope", 4, false}, {"kg CO2e", 1.2, true}}, [][]string{
		{"Scope 1: fuel burnt in the vehicles", kg(g.Scope1Kg)},
		{"Scope 2: electricity used by the vehicles", kg(g.Scope2Kg)},
		{"Scope 3, category 3: producing and delivering the fuel and energy", kg(g.Scope3Cat3Kg)},
		{"Hubs (well-to-wheel, not split by scope)", kg(g.HubsKg)},
	})
}

func distances(l *layout, r *sootcase.Report) {
	l.heading("Distances")
	l.para("Share of tonne-km by the type of distance used (ISO 14083 asks for it to be stated).", pdf.Regular, 8.5, grey)
	l.y += 4
	kinds := []string{"sfd", "gcd", "actual", "unspecified"}
	cols := []col{{"", 1.8, false}}
	for _, k := range kinds {
		cols = append(cols, col{distanceType(k), 1.2, true})
	}
	rowOf := func(name string, m map[string]float64) []string {
		row := []string{name}
		for _, k := range kinds {
			if v, ok := m[k]; ok && v > 0 {
				row = append(row, pct(v))
			} else {
				row = append(row, "")
			}
		}
		return row
	}
	rows := [][]string{}
	for _, m := range r.Modes {
		rows = append(rows, rowOf(modeName(m.Mode), m.DistanceTypes))
	}
	rows = append(rows, rowOf("\x00All transport", r.Total.DistanceTypes))
	l.table(cols, rows)

	if len(r.DAFs) > 0 {
		l.sub("Distance adjustment factors applied")
		rows := [][]string{}
		for _, a := range r.DAFs {
			v := number(a.Min, 3)
			if a.Max != a.Min {
				v = number(a.Min, 3) + " to " + number(a.Max, 3)
			}
			rows = append(rows, []string{a.Source, number(float64(a.Legs), 0), v})
		}
		l.table([]col{{"Source", 4.6, false}, {"Legs", 0.7, true}, {"Factor", 1.2, true}}, rows)
	}
	if c := r.Coverage; c.AddedRoadLegs > 0 {
		l.para(fmt.Sprintf("%s road legs (%s km) were added by the calculator for %s shipments, to and from ports, terminals and airports or across gaps between legs; they are included in the totals.",
			number(float64(c.AddedRoadLegs), 0), number(c.AddedRoadKm, 0), number(float64(c.AddedShipments), 0)), pdf.Regular, 8.5, grey)
	}
}

func dataQuality(l *layout, r *sootcase.Report) {
	l.heading("Data quality")
	dq := r.Data
	shares := []float64{dq.Primary, dq.Modelled, dq.Default}
	names := []string{"Primary data", "Modelled", "Default values"}
	l.need(40)
	x := margin
	for i, s := range shares {
		w := math.Max(0, math.Min(s*contentW, margin+contentW-x)) // rounding can't push past the margin
		l.d.Rect(x, l.y, w, 16, dqCols[i])
		if w > 40 {
			c := white
			if i == 2 {
				c = ink // light grey bar
			}
			l.d.Text(x+5, l.y+11.5, pdf.Bold, 8, c, pct(s))
		}
		x += w
	}
	l.y += 22
	items := make([]string, 3)
	for i := range names {
		items[i] = names[i] + " " + pct(shares[i])
	}
	l.legend(items, dqCols)
	l.para("Shares of total well-to-wheel emissions. Primary: measured fuel or the operator's own values. Modelled: from shipment inputs such as load or energy use. Default: published emission intensities.", pdf.Regular, 8, grey)
	l.y += 4

	if len(dq.DefaultSources) > 0 {
		l.sub("Sources of default values")
		rows := [][]string{}
		for _, s := range dq.DefaultSources {
			rows = append(rows, []string{s.Source, kg(s.WTWKg), pct(s.Share)})
		}
		l.table([]col{{"Source", 5, false}, {"WTW kg CO2e", 1.1, true}, {"Share", 0.7, true}}, rows)
	}
	if len(dq.Models) > 0 {
		l.sub("Models and their parameters")
		rows := [][]string{}
		for _, m := range dq.Models {
			keys := make([]string, 0, len(m.Parameters))
			for k := range m.Parameters {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			ps := make([]string, len(keys))
			for i, k := range keys {
				ps[i] = k + ": " + m.Parameters[k]
			}
			rows = append(rows, []string{m.Model, number(float64(m.Legs), 0), kg(m.WTWKg), strings.Join(ps, "; ")})
		}
		l.table([]col{{"Model", 1.6, false}, {"Legs", 0.6, true}, {"WTW kg CO2e", 1, true}, {"Parameters", 3.6, false}}, rows)
	}
}

func largest(l *layout, r *sootcase.Report) {
	var items []sootcase.Item
	for _, it := range r.Results {
		if it.Result != nil {
			items = append(items, it)
		}
	}
	if len(items) < 2 {
		return
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Result.WTWKg > items[j].Result.WTWKg })
	n := min(10, len(items))
	l.heading(fmt.Sprintf("Largest shipments (%d of %s)", n, number(float64(len(items)), 0)))
	rows := [][]string{}
	for _, it := range items[:n] {
		res := it.Result
		rows = append(rows, []string{it.ShipmentID, route(res.Legs), number(res.WeightKg/1000, 2), number(res.TonneKm, 0),
			kg(res.WTWKg), number(res.IntensityG, 1), share(res.WTWKg, r.Total.WTWKg)})
	}
	l.table([]col{{"Shipment", 1.4, false}, {"Route", 2.6, false}, {"Tonnes", 0.8, true}, {"Tonne-km", 1, true},
		{"WTW kg CO2e", 1.1, true}, {"g/tkm", 0.8, true}, {"Share", 0.7, true}}, rows)
}

func coverage(l *layout, r *sootcase.Report) {
	c := r.Coverage
	l.heading("Coverage")
	rows := [][]string{
		{"Shipments in the input", number(float64(c.Shipments), 0)},
		{"Calculated and included", number(float64(c.Calculated), 0)},
		{"Failed and excluded", number(float64(c.Failed), 0)},
		{"Transport legs", number(float64(c.Legs), 0)},
		{"Hub stops", number(float64(c.HubStops), 0)},
		{"Cargo, tonnes", number(c.WeightT, 1)},
	}
	if c.RetryableFailed > 0 {
		rows = append(rows, []string{"Failures to send again (a routing service didn't answer)", number(float64(c.RetryableFailed), 0)})
	}
	if c.Warned > 0 {
		rows = append(rows, []string{"Shipments with warnings", number(float64(c.Warned), 0)})
	}
	l.table([]col{{"", 4, false}, {"", 1.2, true}}, rows)

	if len(c.FailureReasons) > 0 {
		l.sub("Why shipments failed")
		rows := [][]string{}
		for _, f := range c.FailureReasons {
			rows = append(rows, []string{f.Reason, number(float64(f.Shipments), 0)})
		}
		l.table([]col{{"Reason", 5, false}, {"Shipments", 1, true}}, rows)
	}
	if len(c.Failures) > 0 {
		n := min(20, len(c.Failures))
		l.sub(fmt.Sprintf("Failed shipments (%d of %s)", n, number(float64(c.Failed), 0)))
		rows := [][]string{}
		for _, f := range c.Failures[:n] {
			e := f.Error
			if f.Retryable {
				e += " (send again)"
			}
			rows = append(rows, []string{f.ShipmentID, e})
		}
		l.table([]col{{"Shipment", 1.4, false}, {"Error", 5, false}}, rows)
	}
	if len(c.Warnings) > 0 {
		l.sub("Warnings")
		rows := [][]string{}
		for _, w := range c.Warnings {
			rows = append(rows, []string{w.Reason, number(float64(w.Shipments), 0)})
		}
		l.table([]col{{"Warning", 5, false}, {"Shipments", 1, true}}, rows)
	}
}

func notes(l *layout, r *sootcase.Report) {
	if len(r.Notes) == 0 {
		return
	}
	l.heading("Notes on omissions and deviations")
	l.bullets(r.Notes, 8.5)
}

func method(l *layout, r *sootcase.Report) {
	l.heading("Method and supporting information")
	if r.Reference != "" {
		l.para(r.Reference, pdf.Regular, 8.5, ink)
		l.y += 4
	}
	l.para("Emissions are calculated per leg as tonne-km × emission intensity (× a distance adjustment factor where the distance type differs from the factor's), or from measured fuel or energy where given, and per hub stop as tonnes (or containers) handled × the hub's emission intensity, following the GLEC Framework v3.3 and ISO 14083:2023. "+
		"Every leg's distance, method, data type and factor source are in Sootcase's per-shipment results for the same input. GLEC Framework default values: Smart Freight Centre, used with permission; no affiliation or endorsement is implied.", pdf.Regular, 8.5, grey)
	l.y += 4
	l.para("These are calculations for your own reporting. Sootcase is not certified against ISO 14083; conformity depends on how the data was collected and the boundaries chosen.", pdf.Regular, 8.5, grey)
}

func footers(l *layout, r *sootcase.Report) {
	d := l.d
	n := d.Pages()
	left := "Calculated with Sootcase (sootcase.org)"
	if r.Period.From != "" || r.Period.To != "" {
		left = period(r.Period) + " · " + left
	}
	if r.Organisation != "" {
		left = r.Organisation + " · " + left
	}
	room := contentW - pdf.Width(pdf.Regular, 7.5, fmt.Sprintf("Page %d of %d", n, n)) - 20
	if pdf.Width(pdf.Regular, 7.5, left) > room {
		r := []rune(left)
		for len(r) > 1 && pdf.Width(pdf.Regular, 7.5, string(r)+"…") > room {
			r = r[:len(r)-1]
		}
		left = strings.TrimSpace(string(r)) + "…"
	}
	for i := 0; i < n; i++ {
		d.SetPage(i)
		y := pdf.A4Height - 34
		d.Line(margin, y-10, margin+contentW, y-10, 0.4, faint)
		d.Text(margin, y, pdf.Regular, 7.5, grey, left)
		d.TextRight(margin+contentW, y, pdf.Regular, 7.5, grey, fmt.Sprintf("Page %d of %d", i+1, n))
	}
}

// ---- formatting ----

// number formats v with d decimals and thousands separators.
func number(v float64, d int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return ""
	}
	s := fmt.Sprintf("%.*f", d, v)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i:]
	}
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String() + frac
	if neg && strings.Trim(out, "0.,") != "" {
		out = "-" + out
	}
	return out
}

// kg formats kilograms with sensible precision.
func kg(v float64) string {
	switch a := math.Abs(v); {
	case a == 0:
		return "0"
	case a >= 1000:
		return number(v, 0)
	case a >= 10:
		return number(v, 1)
	default:
		return number(v, 2)
	}
}

// tonnes formats kilograms as tonnes.
func tonnes(v float64) string {
	t := v / 1000
	switch a := math.Abs(t); {
	case a == 0:
		return "0"
	case a >= 1000:
		return number(t, 0)
	case a >= 10:
		return number(t, 1)
	case a >= 0.1:
		return number(t, 2)
	default:
		return number(t, 3)
	}
}

func pct(v float64) string {
	p := v * 100
	if p > 0 && p < 0.1 {
		return "<0.1%"
	}
	if p >= 10 {
		return number(p, 0) + "%"
	}
	return number(p, 1) + "%"
}

func share(v, total float64) string {
	if total == 0 {
		return ""
	}
	return pct(v / total)
}

func plural(n int, what string) string {
	if n == 0 {
		return "none " + what
	}
	return number(float64(n), 0) + " " + what
}

func period(p sootcase.Period) string {
	f := func(s string) string {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t.Format("2 Jan 2006")
		}
		return s
	}
	switch {
	case p.From != "" && p.To != "":
		return f(p.From) + " to " + f(p.To)
	case p.From != "":
		return "from " + f(p.From)
	case p.To != "":
		return "until " + f(p.To)
	}
	return "not stated"
}

func level(s string) string {
	if s == "transport_or_hub_services" {
		return "Transport or hub services (GLEC Framework v3.3, Section 2, Chapter 1)"
	}
	return label(s)
}

// scope3Label is "Scope 3, category 4 (upstream transportation and distribution)".
func scope3Label(c int) string {
	s := fmt.Sprintf("Scope 3, category %d", c)
	if n := scope3Name(c); n != "" {
		s += " (" + n + ")"
	}
	return s
}

func scope3Name(c int) string {
	switch c {
	case 4:
		return "upstream transportation and distribution"
	case 9:
		return "downstream transportation and distribution"
	}
	return ""
}

func modeName(m string) string {
	switch m {
	case "inland_waterway":
		return "Inland waterway"
	case "":
		return "Unknown"
	}
	return strings.ToUpper(m[:1]) + m[1:]
}

func distanceType(k string) string {
	switch k {
	case "sfd":
		return "Shortest feasible"
	case "gcd":
		return "Great circle"
	case "actual":
		return "Actual"
	case "unspecified":
		return "Not stated"
	}
	return k
}

// label turns an id like "storage_transshipment" into "Storage transshipment".
func label(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// route is "From → To" over a shipment's legs, when the places have names or codes.
func route(legs []sootcase.Leg) string {
	name := func(p *sootcase.Place) string {
		if p == nil {
			return ""
		}
		if p.Name != "" {
			return p.Name
		}
		return p.Code
	}
	var modes []string
	for _, lg := range legs {
		if len(modes) == 0 || modes[len(modes)-1] != lg.Mode {
			modes = append(modes, lg.Mode)
		}
	}
	m := strings.ReplaceAll(strings.Join(modes, ", "), "inland_waterway", "waterway")
	if len(legs) == 0 {
		return ""
	}
	from, to := name(legs[0].From), name(legs[len(legs)-1].To)
	if from != "" && to != "" {
		return from + " → " + to + " (" + m + ")"
	}
	return m
}
