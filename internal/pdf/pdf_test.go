package pdf

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestWidth(t *testing.T) {
	// Helvetica: "H" 722, "i" 222; bold "H" 722, "i" 278.
	if w := Width(Regular, 10, "Hi"); w != 9.44 {
		t.Errorf("regular width = %v, want 9.44", w)
	}
	if w := Width(Bold, 10, "Hi"); w != 10 {
		t.Errorf("bold width = %v, want 10", w)
	}
}

func TestWrap(t *testing.T) {
	lines := Wrap(Regular, 10, "one two three four five six seven", 60)
	for _, l := range lines {
		if Width(Regular, 10, l) > 60 {
			t.Errorf("line %q is %v wide", l, Width(Regular, 10, l))
		}
	}
	if strings.Join(lines, " ") != "one two three four five six seven" {
		t.Errorf("words lost: %q", lines)
	}
	long := Wrap(Regular, 10, strings.Repeat("x", 100), 50)
	if len(long) < 2 || strings.Join(long, "") != strings.Repeat("x", 100) {
		t.Errorf("long word: %q", long)
	}
	if got := Wrap(Regular, 10, "a\n\nb", 100); len(got) != 3 {
		t.Errorf("newlines: %q", got)
	}
}

func TestEncode(t *testing.T) {
	got := string(Encode("Århus – 5 €, CO₂ ≥ 1 中"))
	want := "\xc5rhus \x96 5 \x80, CO2 >= 1 ?"
	if got != want {
		t.Errorf("Encode = %q, want %q", got, want)
	}
}

func TestDocumentStructure(t *testing.T) {
	d := New()
	d.Title = "Test – æøå"
	d.AddPage()
	d.Text(50, 60, Bold, 18, Hex("#1F5E8C"), "Hello (world) \\ København")
	d.Rect(50, 80, 100, 20, Hex("#cccccc"))
	d.Line(50, 110, 200, 110, 0.5, Color{})
	d.AddPage()
	d.Text(50, 60, Regular, 10, Color{}, "Page two")
	b := d.Bytes()

	if !bytes.HasPrefix(b, []byte("%PDF-1.4")) || !bytes.HasSuffix(b, []byte("%%EOF\n")) {
		t.Fatal("not a PDF")
	}
	if !bytes.Contains(b, []byte("/Count 2")) {
		t.Error("expected 2 pages")
	}
	// Every xref offset must point at its object.
	m := regexp.MustCompile(`startxref\n(\d+)`).FindSubmatch(b)
	xref, _ := strconv.Atoi(string(m[1]))
	entries := strings.Split(string(b[xref:]), "\n")[3:]
	for i, e := range entries {
		if !strings.HasSuffix(e, " n ") {
			break
		}
		off, _ := strconv.Atoi(e[:10])
		if !bytes.HasPrefix(b[off:], []byte(strconv.Itoa(i+1)+" 0 obj")) {
			t.Errorf("xref entry %d points at %q", i+1, b[off:off+10])
		}
	}

	// With poppler installed, check the text comes back out.
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	f := filepath.Join(t.TempDir(), "t.pdf")
	os.WriteFile(f, b, 0o644)
	out, err := exec.Command("pdftotext", f, "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"Hello (world) \\ København", "Page two"} {
		if !strings.Contains(string(out), s) {
			t.Errorf("text %q not in %q", s, out)
		}
	}
}
