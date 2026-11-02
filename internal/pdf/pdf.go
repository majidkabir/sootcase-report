// Package pdf writes simple PDF documents: text in Helvetica, filled
// rectangles and lines, on A4 pages. It has no dependencies outside the
// standard library.
//
// Text is encoded as WinAnsi (Windows-1252), which covers Western European
// languages; characters outside it are written as '?'. Coordinates are in
// points from the top-left corner of the page.
package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// A4 in points.
const (
	A4Width  = 595.28
	A4Height = 841.89
)

// Font is one of the two built-in fonts.
type Font int

const (
	Regular Font = iota // Helvetica
	Bold                // Helvetica-Bold
)

// Color is an RGB color, each component 0 to 1.
type Color struct{ R, G, B float64 }

// Hex parses "#rrggbb".
func Hex(s string) Color {
	s = strings.TrimPrefix(s, "#")
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil || len(s) != 6 {
		return Color{}
	}
	return Color{float64(v>>16&255) / 255, float64(v>>8&255) / 255, float64(v&255) / 255}
}

// Doc is a document being written.
type Doc struct {
	Title   string
	Author  string
	Subject string
	Created time.Time

	pages []*bytes.Buffer
	cur   int
}

// New returns an empty document; call AddPage before drawing.
func New() *Doc { return &Doc{cur: -1} }

// AddPage starts a new page and makes it current.
func (d *Doc) AddPage() {
	d.pages = append(d.pages, &bytes.Buffer{})
	d.cur = len(d.pages) - 1
}

// Pages is the number of pages so far.
func (d *Doc) Pages() int { return len(d.pages) }

// SetPage makes page i (0-based) current, to draw on it again.
func (d *Doc) SetPage(i int) { d.cur = i }

func (d *Doc) w() *bytes.Buffer {
	if d.cur < 0 {
		d.AddPage()
	}
	return d.pages[d.cur]
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func (c Color) fill() string   { return num3(c.R) + " " + num3(c.G) + " " + num3(c.B) + " rg" }
func (c Color) stroke() string { return num3(c.R) + " " + num3(c.G) + " " + num3(c.B) + " RG" }
func num3(v float64) string    { return strconv.FormatFloat(v, 'f', 3, 64) }

// Text writes s with its baseline at y.
func (d *Doc) Text(x, y float64, f Font, size float64, c Color, s string) {
	if s == "" {
		return
	}
	fmt.Fprintf(d.w(), "BT /F%d %s Tf %s %s %s Td (%s) Tj ET\n",
		int(f)+1, num(size), c.fill(), num(x), num(A4Height-y), escape(Encode(s)))
}

// TextRight writes s ending at x.
func (d *Doc) TextRight(x, y float64, f Font, size float64, c Color, s string) {
	d.Text(x-Width(f, size, s), y, f, size, c, s)
}

// Rect fills a rectangle whose top-left corner is (x, y).
func (d *Doc) Rect(x, y, w, h float64, c Color) {
	if w <= 0 || h <= 0 {
		return
	}
	fmt.Fprintf(d.w(), "%s %s %s %s %s re f\n", c.fill(), num(x), num(A4Height-y-h), num(w), num(h))
}

// Line draws a line.
func (d *Doc) Line(x1, y1, x2, y2, width float64, c Color) {
	fmt.Fprintf(d.w(), "%s %s w %s %s m %s %s l S\n", c.stroke(), num(width),
		num(x1), num(A4Height-y1), num(x2), num(A4Height-y2))
}

// Width is the width of s in points.
func Width(f Font, size float64, s string) float64 {
	tbl := &helveticaWidths
	if f == Bold {
		tbl = &helveticaBoldWidths
	}
	var w int
	for _, b := range Encode(s) {
		w += int(tbl[b])
	}
	return float64(w) * size / 1000
}

// Wrap breaks s into lines no wider than maxW, at spaces where it can and
// inside words that are wider than a line. Newlines in s start new lines.
func Wrap(f Font, size float64, s string, maxW float64) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range words {
			try := word
			if line != "" {
				try = line + " " + word
			}
			if Width(f, size, try) <= maxW {
				line = try
				continue
			}
			if line != "" {
				out = append(out, line)
			}
			// A word wider than the line is split.
			for Width(f, size, word) > maxW {
				r := []rune(word)
				n := len(r) - 1
				for n > 1 && Width(f, size, string(r[:n])) > maxW {
					n--
				}
				out = append(out, string(r[:n]))
				word = string(r[n:])
			}
			line = word
		}
		out = append(out, line)
	}
	return out
}

// cp1252 maps the Windows-1252 characters between 0x80 and 0x9f.
var cp1252 = map[rune]byte{
	'€': 0x80, '‚': 0x82, 'ƒ': 0x83, '„': 0x84, '…': 0x85, '†': 0x86, '‡': 0x87, 'ˆ': 0x88,
	'‰': 0x89, 'Š': 0x8a, '‹': 0x8b, 'Œ': 0x8c, 'Ž': 0x8e, '‘': 0x91, '’': 0x92, '“': 0x93,
	'”': 0x94, '•': 0x95, '–': 0x96, '—': 0x97, '˜': 0x98, '™': 0x99, 'š': 0x9a, '›': 0x9b,
	'œ': 0x9c, 'ž': 0x9e, 'Ÿ': 0x9f,
}

// Look-alikes for common characters outside Windows-1252.
var lookalike = map[rune]string{
	'≥': ">=", '≤': "<=", '≈': "~", '−': "-", '‑': "-", '→': "->", '←': "<-",
	'₀': "0", '₁': "1", '₂': "2", '₃': "3", '₄': "4", ' ': " ", ' ': " ", '​': "",
}

// Encode converts s to Windows-1252 bytes.
func Encode(s string) []byte {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b = append(b, ' ')
		case r >= 0x20 && r < 0x7f, r >= 0xa0 && r <= 0xff:
			b = append(b, byte(r))
		case cp1252[r] != 0:
			b = append(b, cp1252[r])
		case lookalike[r] != "" || r == '​':
			b = append(b, lookalike[r]...)
		case unicode.IsControl(r):
		default:
			b = append(b, '?')
		}
	}
	return b
}

func escape(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		switch {
		case c == '(' || c == ')' || c == '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case c < 0x20 || c > 0x7e:
			fmt.Fprintf(&sb, "\\%03o", c)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// pdfString is a PDF text string for the document information: ASCII as
// is, otherwise UTF-16BE with a byte order mark.
func pdfString(s string) string {
	ascii := true
	for _, r := range s {
		if r > 0x7e || r < 0x20 {
			ascii = false
			break
		}
	}
	if ascii {
		return "(" + escape([]byte(s)) + ")"
	}
	var sb strings.Builder
	sb.WriteString("<FEFF")
	for _, r := range s {
		if r > 0xffff {
			r -= 0x10000
			fmt.Fprintf(&sb, "%04X%04X", 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			continue
		}
		fmt.Fprintf(&sb, "%04X", r)
	}
	sb.WriteString(">")
	return sb.String()
}

// Bytes returns the finished PDF.
func (d *Doc) Bytes() []byte {
	if len(d.pages) == 0 {
		d.AddPage()
	}
	var out bytes.Buffer
	var offsets []int
	obj := func(body string) int {
		offsets = append(offsets, out.Len())
		n := len(offsets)
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", n, body)
		return n
	}
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

	// Fixed numbers: 1 catalog, 2 page tree, 3 and 4 fonts, 5 info.
	nPages := len(d.pages)
	firstPage := 6
	kids := make([]string, nPages)
	for i := range kids {
		kids[i] = fmt.Sprintf("%d 0 R", firstPage+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), nPages))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	created := d.Created
	if created.IsZero() {
		created = time.Now()
	}
	info := fmt.Sprintf("<< /Producer (sootcase-report) /CreationDate (D:%s) /Title %s /Author %s /Subject %s >>",
		created.UTC().Format("20060102150405Z"), pdfString(d.Title), pdfString(d.Author), pdfString(d.Subject))
	obj(info)

	for _, p := range d.pages {
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		zw.Write(p.Bytes())
		zw.Close()
		content := len(offsets) + 2
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %s %s] "+
			"/Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>",
			num(A4Width), num(A4Height), content))
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", len(offsets), z.Len())
		out.Write(z.Bytes())
		out.WriteString("\nendstream\nendobj\n")
	}

	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info 5 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return out.Bytes()
}
