// Package compare diffs two Kismet captures.
package compare

import (
	"cmp"
	"slices"

	"github.com/Halcy0nic/SignalSift/internal/kismet"
)

// Options controls which devices appear in a Report.
type Options struct {
	Top        int   // Devices listed per capture in the most-active tables.
	MinPackets int64 // Devices with fewer packets are left out of every list.
}

// Report is the result of comparing a baseline capture to a follow-up.
type Report struct {
	Baseline kismet.Summary `json:"baseline"`
	Followup kismet.Summary `json:"followup"`

	Top        int   `json:"top"`
	MinPackets int64 `json:"min_packets"`

	// NewTotal and MissingTotal count distinct MACs before MinPackets applies.
	NewTotal     int `json:"new_total"`
	MissingTotal int `json:"missing_total"`

	TopBaseline []kismet.Device `json:"top_baseline"`
	TopFollowup []kismet.Device `json:"top_followup"`
	New         []kismet.Device `json:"new"`
	Missing     []kismet.Device `json:"missing"`
}

// Build compares two captures. Devices are matched by MAC alone, so a device
// picked up by a different radio (Bluetooth versus BTLE, say) is not reported
// as both new and missing. Every record of an unmatched MAC is listed.
func Build(baseline, followup *kismet.Capture, opts Options) Report {
	added := absent(followup.Devices, baseline.Devices)
	removed := absent(baseline.Devices, followup.Devices)

	return Report{
		Baseline:     baseline.Summary,
		Followup:     followup.Summary,
		Top:          opts.Top,
		MinPackets:   opts.MinPackets,
		NewTotal:     distinctMACs(added),
		MissingTotal: distinctMACs(removed),
		TopBaseline:  top(baseline.Devices, opts),
		TopFollowup:  top(followup.Devices, opts),
		New:          filter(added, opts.MinPackets),
		Missing:      filter(removed, opts.MinPackets),
	}
}

// absent returns the devices whose MAC does not appear in other.
func absent(devices, other []kismet.Device) []kismet.Device {
	seen := make(map[string]bool, len(other))
	for _, d := range other {
		seen[d.MAC] = true
	}
	result := []kismet.Device{}
	for _, d := range devices {
		if !seen[d.MAC] {
			result = append(result, d)
		}
	}
	return result
}

func distinctMACs(devices []kismet.Device) int {
	seen := make(map[string]bool, len(devices))
	for _, d := range devices {
		seen[d.MAC] = true
	}
	return len(seen)
}

func top(devices []kismet.Device, opts Options) []kismet.Device {
	sorted := filter(devices, opts.MinPackets)
	return sorted[:min(opts.Top, len(sorted))]
}

// filter returns a new slice of devices with at least minPackets packets,
// busiest first. Ties are broken by MAC then PHY so output is repeatable.
func filter(devices []kismet.Device, minPackets int64) []kismet.Device {
	result := []kismet.Device{}
	for _, d := range devices {
		if d.Packets >= minPackets {
			result = append(result, d)
		}
	}
	slices.SortFunc(result, func(a, b kismet.Device) int {
		return cmp.Or(
			cmp.Compare(b.Packets, a.Packets),
			cmp.Compare(a.MAC, b.MAC),
			cmp.Compare(a.PHY, b.PHY),
		)
	})
	return result
}
