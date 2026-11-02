package render

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Halcy0nic/SignalSift/internal/compare"
	"github.com/Halcy0nic/SignalSift/internal/kismet"
)

func sampleReport() compare.Report {
	signal := -70
	start := time.Unix(1728088796, 0)
	end := start.Add(time.Minute)
	return compare.Report{
		Baseline:     kismet.Summary{File: "/caps/base.kismet", Start: &start, End: &end, DurationSeconds: 60},
		Followup:     kismet.Summary{File: "/caps/follow.kismet"},
		Top:          10,
		NewTotal:     1,
		MissingTotal: 0,
		New: []kismet.Device{{
			MAC: "AA:BB:CC:DD:EE:FF", Name: "=cmd|'/c calc'!A1", PHY: "IEEE802.11",
			Type: "Wi-Fi AP", Signal: &signal, Packets: 7,
		}},
		Missing: []kismet.Device{},
	}
}

func TestTableHasNoEscapesWhenPiped(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "\x1b") {
		t.Error("escape sequences written to a non-terminal")
	}
	for _, want := range []string{"base.kismet", "AA:BB:CC:DD:EE:FF", "New Devices: 1", "Missing Devices: 0", "no packets", "None"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/caps/") {
		t.Error("table should show base names only")
	}
}

func TestTableOmitsTopListsWhenDisabled(t *testing.T) {
	r := sampleReport()
	r.Top = 0
	var buf bytes.Buffer
	if err := Table(&buf, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "Most Active") {
		t.Error("--top 0 should hide the most-active lists")
	}
}

func TestDisplayNameTruncates(t *testing.T) {
	if got := displayName(strings.Repeat("日", 40)); len([]rune(got)) != maxNameWidth {
		t.Errorf("got %d runes", len([]rune(got)))
	}
	if got := displayName(""); got != "-" {
		t.Errorf("empty name = %q", got)
	}
}

func TestCountLabel(t *testing.T) {
	tests := []struct {
		total, shown int
		min          int64
		want         string
	}{
		{5, 5, 0, "5"},
		{5, 7, 0, "5 (7 records across PHYs)"},
		{5, 2, 10, "5 (showing 2 records with at least 10 packets)"},
	}
	for _, tt := range tests {
		if got := countLabel(tt.total, tt.shown, tt.min); got != tt.want {
			t.Errorf("countLabel(%d,%d,%d) = %q, want %q", tt.total, tt.shown, tt.min, got, tt.want)
		}
	}
}

func TestJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var got struct {
		NewTotal int `json:"new_total"`
		New      []struct {
			MAC    string `json:"mac"`
			Signal *int   `json:"signal"`
		} `json:"new"`
		Missing []any `json:"missing"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.NewTotal != 1 || got.New[0].MAC != "AA:BB:CC:DD:EE:FF" || *got.New[0].Signal != -70 {
		t.Errorf("decoded = %+v", got)
	}
	if got.Missing == nil {
		t.Error("empty list encoded as null")
	}
}

func TestCSVGuardsAgainstFormulas(t *testing.T) {
	var buf bytes.Buffer
	if err := CSV(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want header and one device", len(rows))
	}
	row := rows[1]
	if row[0] != "new" {
		t.Errorf("section = %q", row[0])
	}
	if row[2] != "'=cmd|'/c calc'!A1" {
		t.Errorf("name not neutralised: %q", row[2])
	}
	if row[8] != "-70" {
		t.Errorf("signal should stay numeric: %q", row[8])
	}
}

func TestCSVCell(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "Home": "Home", "=1+1": "'=1+1", "+1": "'+1", "-1": "'-1", "@x": "'@x", "\tx": "'\tx",
	} {
		if got := csvCell(in); got != want {
			t.Errorf("csvCell(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHTMLEscapesNames(t *testing.T) {
	r := sampleReport()
	r.New[0].Name = `<script>alert(1)</script>`
	var buf bytes.Buffer
	if err := HTML(&buf, r, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "<script") {
		t.Error("over-the-air name reached the page as markup")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("escaped name missing from page")
	}
	for _, external := range []string{"http://", "https://", "src="} {
		if strings.Contains(out, external) {
			t.Errorf("report should be self-contained, found %q", external)
		}
	}
}

func TestWriteRejectsUnknownFormat(t *testing.T) {
	if err := Write(&bytes.Buffer{}, "xml", sampleReport()); err == nil {
		t.Error("expected an error")
	}
}
