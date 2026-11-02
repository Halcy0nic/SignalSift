package render

import (
	_ "embed"
	"html/template"
	"io"
	"path/filepath"
	"time"

	"github.com/Halcy0nic/SignalSift/internal/compare"
	"github.com/Halcy0nic/SignalSift/internal/kismet"
)

//go:embed templates/report.html.tmpl
var reportSource string

var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"base":     filepath.Base,
	"time":     formatTime,
	"duration": formatDuration,
	"signal":   signalValue,
	"dash": func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	},
}).Parse(reportSource))

type deviceList struct {
	Title   string
	Kind    string
	Devices []kismet.Device
}

type htmlData struct {
	compare.Report
	Generated string
	Lists     []deviceList
}

// HTML writes a self-contained report with no scripts or external assets.
func HTML(w io.Writer, r compare.Report, generated time.Time) error {
	lists := []deviceList{
		{"New Devices: " + countLabel(r.NewTotal, len(r.New), r.MinPackets), "new", r.New},
		{"Missing Devices: " + countLabel(r.MissingTotal, len(r.Missing), r.MinPackets), "missing", r.Missing},
	}
	if r.Top > 0 {
		lists = append(lists,
			deviceList{"Most Active Devices: Baseline", "top", r.TopBaseline},
			deviceList{"Most Active Devices: Follow-up", "top", r.TopFollowup},
		)
	}
	return reportTemplate.Execute(w, htmlData{
		Report:    r,
		Generated: generated.Format(timeLayout),
		Lists:     lists,
	})
}
