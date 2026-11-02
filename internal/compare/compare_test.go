package compare

import (
	"testing"

	"github.com/Halcy0nic/SignalSift/internal/kismet"
)

func dev(mac, phy string, packets int64) kismet.Device {
	return kismet.Device{MAC: mac, PHY: phy, Packets: packets}
}

func capture(devices ...kismet.Device) *kismet.Capture {
	return &kismet.Capture{Devices: devices}
}

func macs(devices []kismet.Device) []string {
	out := make([]string, len(devices))
	for i, d := range devices {
		out[i] = d.MAC
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNewAndMissing(t *testing.T) {
	base := capture(dev("A", "wifi", 1), dev("B", "wifi", 1))
	follow := capture(dev("B", "wifi", 1), dev("C", "wifi", 1))

	r := Build(base, follow, Options{Top: 10})
	if r.NewTotal != 1 || !equal(macs(r.New), []string{"C"}) {
		t.Errorf("new = %d %v", r.NewTotal, macs(r.New))
	}
	if r.MissingTotal != 1 || !equal(macs(r.Missing), []string{"A"}) {
		t.Errorf("missing = %d %v", r.MissingTotal, macs(r.Missing))
	}
}

func TestPHYChangeIsNotReportedAsADifference(t *testing.T) {
	r := Build(capture(dev("A", "Bluetooth", 5)), capture(dev("A", "BTLE", 5)), Options{})
	if r.NewTotal != 0 || r.MissingTotal != 0 {
		t.Errorf("same MAC on another PHY: new=%d missing=%d", r.NewTotal, r.MissingTotal)
	}
}

func TestNewMACOnTwoPHYsCountsOnceButListsBoth(t *testing.T) {
	r := Build(capture(), capture(dev("A", "Bluetooth", 5), dev("A", "BTLE", 9)), Options{})
	if r.NewTotal != 1 {
		t.Errorf("NewTotal = %d, want 1", r.NewTotal)
	}
	if len(r.New) != 2 {
		t.Errorf("listed %d records, want 2", len(r.New))
	}
}

func TestListsSortedRepeatably(t *testing.T) {
	follow := capture(dev("B", "x", 5), dev("C", "x", 9), dev("A", "z", 5), dev("A", "y", 5))
	want := []kismet.Device{dev("C", "x", 9), dev("A", "y", 5), dev("A", "z", 5), dev("B", "x", 5)}

	r := Build(capture(), follow, Options{Top: 10})
	for name, got := range map[string][]kismet.Device{"new": r.New, "top": r.TopFollowup} {
		for i := range want {
			if got[i].MAC != want[i].MAC || got[i].PHY != want[i].PHY {
				t.Errorf("%s[%d] = %+v, want %+v", name, i, got[i], want[i])
			}
		}
	}
}

func TestTopLimit(t *testing.T) {
	c := capture(dev("A", "x", 1), dev("B", "x", 2), dev("C", "x", 3))
	tests := []struct {
		top  int
		want []string
	}{
		{0, []string{}},
		{2, []string{"C", "B"}},
		{50, []string{"C", "B", "A"}},
	}
	for _, tt := range tests {
		if got := macs(Build(c, c, Options{Top: tt.top}).TopBaseline); !equal(got, tt.want) {
			t.Errorf("top %d = %v, want %v", tt.top, got, tt.want)
		}
	}
}

func TestMinPacketsFiltersListsButNotTotals(t *testing.T) {
	base := capture(dev("A", "x", 1), dev("B", "x", 50))
	follow := capture(dev("C", "x", 2), dev("D", "x", 60))

	r := Build(base, follow, Options{Top: 10, MinPackets: 10})
	if r.NewTotal != 2 || r.MissingTotal != 2 {
		t.Errorf("totals = %d/%d, want 2/2", r.NewTotal, r.MissingTotal)
	}
	if !equal(macs(r.New), []string{"D"}) || !equal(macs(r.Missing), []string{"B"}) {
		t.Errorf("new=%v missing=%v", macs(r.New), macs(r.Missing))
	}
	if !equal(macs(r.TopFollowup), []string{"D"}) {
		t.Errorf("top = %v", macs(r.TopFollowup))
	}
}

func TestEmptyListsAreNotNil(t *testing.T) {
	r := Build(capture(), capture(), Options{Top: 10})
	if r.New == nil || r.Missing == nil || r.TopBaseline == nil || r.TopFollowup == nil {
		t.Errorf("nil list would encode as JSON null: %+v", r)
	}
}
