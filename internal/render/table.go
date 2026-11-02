package render

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/Halcy0nic/SignalSift/internal/compare"
	"github.com/Halcy0nic/SignalSift/internal/kismet"
)

const maxNameWidth = 32

// Table writes a terminal report. Colour is used only when w is a terminal.
func Table(w io.Writer, r compare.Report) error {
	re := lipgloss.NewRenderer(w)
	pal := palette{
		accent: lipgloss.AdaptiveColor{Light: "#0B6E99", Dark: "#5FD7FF"},
		good:   lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#5FD787"},
		bad:    lipgloss.AdaptiveColor{Light: "#B3261E", Dark: "#FF875F"},
		dim:    lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#7A8490"},
	}

	blocks := []string{
		heading(re, pal.accent, "Capture Summary"),
		summaryTable(re, pal, r),
	}
	if r.Top > 0 {
		blocks = append(blocks,
			heading(re, pal.accent, fmt.Sprintf("Top %d Most Active Devices: Baseline", r.Top)),
			deviceTable(re, pal, pal.accent, r.TopBaseline),
			heading(re, pal.accent, fmt.Sprintf("Top %d Most Active Devices: Follow-up", r.Top)),
			deviceTable(re, pal, pal.accent, r.TopFollowup),
		)
	}
	blocks = append(blocks,
		heading(re, pal.good, "New Devices: "+countLabel(r.NewTotal, len(r.New), r.MinPackets)),
		deviceTable(re, pal, pal.good, r.New),
		heading(re, pal.bad, "Missing Devices: "+countLabel(r.MissingTotal, len(r.Missing), r.MinPackets)),
		deviceTable(re, pal, pal.bad, r.Missing),
	)

	for _, block := range blocks {
		if _, err := fmt.Fprintln(w, block); err != nil {
			return err
		}
	}
	return nil
}

type palette struct {
	accent, good, bad, dim lipgloss.AdaptiveColor
}

func heading(re *lipgloss.Renderer, color lipgloss.TerminalColor, text string) string {
	return re.NewStyle().Bold(true).Foreground(color).MarginTop(1).Render(text)
}

func summaryTable(re *lipgloss.Renderer, pal palette, r compare.Report) string {
	b, f := r.Baseline, r.Followup
	rows := [][]string{
		{"File", filepath.Base(b.File), filepath.Base(f.File)},
		{"Kismet version", b.KismetVersion, f.KismetVersion},
		{"Start", formatTime(b.Start), formatTime(f.Start)},
		{"End", formatTime(b.End), formatTime(f.End)},
		{"Duration", formatDuration(b.DurationSeconds), formatDuration(f.DurationSeconds)},
		{"Unique devices", strconv.Itoa(b.UniqueDevices), strconv.Itoa(f.UniqueDevices)},
		{"Packets", strconv.FormatInt(b.PacketCount, 10), strconv.FormatInt(f.PacketCount, 10)},
	}
	return baseTable(re, pal, pal.accent, nil).
		Headers("", "Baseline", "Follow-up").
		Rows(rows...).
		Render()
}

func deviceTable(re *lipgloss.Renderer, pal palette, color lipgloss.TerminalColor, devices []kismet.Device) string {
	if len(devices) == 0 {
		return re.NewStyle().Foreground(pal.dim).Render("None")
	}
	rows := make([][]string, 0, len(devices))
	for _, d := range devices {
		signal := signalValue(d)
		if signal == "" {
			signal = "-"
		}
		rows = append(rows, []string{
			d.MAC, displayName(d.Name), d.PHY, d.Type, signal, strconv.FormatInt(d.Packets, 10),
		})
	}
	const signalCol, packetsCol = 4, 5
	return baseTable(re, pal, color, []int{signalCol, packetsCol}).
		Headers("MAC", "Name", "PHY", "Type", "Signal", "Packets").
		Rows(rows...).
		Render()
}

func baseTable(re *lipgloss.Renderer, pal palette, color lipgloss.TerminalColor, rightAligned []int) *table.Table {
	cell := re.NewStyle().Padding(0, 1)
	header := cell.Bold(true).Foreground(color)
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(re.NewStyle().Foreground(pal.dim)).
		StyleFunc(func(row, col int) lipgloss.Style {
			style := cell
			if row == table.HeaderRow {
				style = header
			}
			for _, c := range rightAligned {
				if c == col {
					return style.Align(lipgloss.Right)
				}
			}
			return style
		})
}

// displayName keeps one long Bluetooth name from pushing the table off screen.
func displayName(name string) string {
	if name == "" {
		return "-"
	}
	runes := []rune(name)
	if len(runes) > maxNameWidth {
		return string(runes[:maxNameWidth-1]) + "…"
	}
	return name
}
