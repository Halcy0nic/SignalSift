// Package render writes comparison reports as tables, JSON, CSV and HTML.
package render

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Halcy0nic/SignalSift/internal/compare"
	"github.com/Halcy0nic/SignalSift/internal/kismet"
)

const timeLayout = "2006-01-02 15:04:05"

// Write renders r to w in the named format: table, json or csv.
func Write(w io.Writer, format string, r compare.Report) error {
	switch format {
	case "table":
		return Table(w, r)
	case "json":
		return JSON(w, r)
	case "csv":
		return CSV(w, r)
	}
	return fmt.Errorf("unknown format %q (want table, json or csv)", format)
}

// JSON writes the report as indented JSON.
func JSON(w io.Writer, r compare.Report) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// CSV writes every listed device as one row, with a section column naming the
// list it came from: top_baseline, top_followup, new or missing.
func CSV(w io.Writer, r compare.Report) error {
	cw := csv.NewWriter(w)
	err := cw.Write([]string{
		"section", "mac", "name", "phy", "type", "manufacturer", "channel",
		"encryption", "signal", "packets", "first_seen", "last_seen",
	})
	if err != nil {
		return err
	}
	for _, s := range sections(r) {
		for _, d := range s.devices {
			err := cw.Write([]string{
				s.id, d.MAC, csvCell(d.Name), csvCell(d.PHY), csvCell(d.Type),
				csvCell(d.Manufacturer), csvCell(d.Channel), csvCell(d.Encryption),
				signalValue(d), strconv.FormatInt(d.Packets, 10),
				d.FirstSeen.Format(time.RFC3339), d.LastSeen.Format(time.RFC3339),
			})
			if err != nil {
				return err
			}
		}
	}
	cw.Flush()
	return cw.Error()
}

// csvCell stops spreadsheets from running an over-the-air name as a formula.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func signalValue(d kismet.Device) string {
	if d.Signal == nil {
		return ""
	}
	return strconv.Itoa(*d.Signal)
}

type section struct {
	id      string
	devices []kismet.Device
}

func sections(r compare.Report) []section {
	return []section{
		{"top_baseline", r.TopBaseline},
		{"top_followup", r.TopFollowup},
		{"new", r.New},
		{"missing", r.Missing},
	}
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "no packets"
	}
	return t.Format(timeLayout)
}

func formatDuration(seconds int64) string {
	return (time.Duration(seconds) * time.Second).String()
}

// countLabel describes a new/missing total of distinct MACs. It explains when
// the row count differs: a filter hides rows, and a MAC seen on several PHYs
// has a row for each.
func countLabel(total, shown int, minPackets int64) string {
	switch {
	case minPackets > 0:
		return fmt.Sprintf("%d (showing %d records with at least %d packets)", total, shown, minPackets)
	case shown != total:
		return fmt.Sprintf("%d (%d records across PHYs)", total, shown)
	}
	return strconv.Itoa(total)
}
