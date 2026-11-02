package kismet

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const schema = `
CREATE TABLE KISMET (kismet_version TEXT, db_version INT, db_module TEXT);
CREATE TABLE devices (first_time INT, last_time INT, devkey TEXT, phyname TEXT, devmac TEXT,
	strongest_signal INT, type TEXT, device BLOB, UNIQUE(phyname, devmac) ON CONFLICT REPLACE);
CREATE TABLE packets (ts_sec INT, phyname TEXT, sourcemac TEXT);
INSERT INTO KISMET VALUES ('2024.10.0', 8, 'kismetlog');`

type fixture struct {
	t  *testing.T
	db *sql.DB
}

func newFixture(t *testing.T, path string) *fixture {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return &fixture{t, db}
}

func (f *fixture) device(phy, mac string, signal int, blob any) {
	f.t.Helper()
	data, ok := blob.([]byte)
	if !ok {
		var err error
		if data, err = json.Marshal(blob); err != nil {
			f.t.Fatal(err)
		}
	}
	_, err := f.db.Exec(`INSERT INTO devices VALUES (100, 200, 'key', ?, ?, ?, 'Wi-Fi AP', ?)`,
		phy, mac, signal, data)
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) packets(phy, mac string, ts ...int) {
	f.t.Helper()
	for _, v := range ts {
		if _, err := f.db.Exec(`INSERT INTO packets VALUES (?, ?, ?)`, v, phy, mac); err != nil {
			f.t.Fatal(err)
		}
	}
}

func blob(name string, packets int) map[string]any {
	return map[string]any{
		"kismet.device.base.commonname":    name,
		"kismet.device.base.packets.total": packets,
	}
}

func find(t *testing.T, c *Capture, phy, mac string) Device {
	t.Helper()
	for _, d := range c.Devices {
		if d.PHY == phy && d.MAC == mac {
			return d
		}
	}
	t.Fatalf("device %s/%s not found", phy, mac)
	return Device{}
}

func TestLoadSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.kismet")
	f := newFixture(t, path)
	f.device("IEEE802.11", "AA:AA:AA:AA:AA:AA", -40, blob("Home", 1))
	f.device("Bluetooth", "AA:AA:AA:AA:AA:AA", 0, blob("Home", 1))
	f.device("BTLE", "BB:BB:BB:BB:BB:BB", -50, blob("", 1))
	f.packets("IEEE802.11", "AA:AA:AA:AA:AA:AA", 1000, 1600)
	f.packets("Unknown", "00:00:00:00:00:00", 1300)

	c, err := Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	s := c.Summary
	if s.PacketCount != 3 || s.DurationSeconds != 600 || s.KismetVersion != "2024.10.0" {
		t.Errorf("summary = %+v", s)
	}
	if s.UniqueDevices != 2 {
		t.Errorf("UniqueDevices = %d, want 2 distinct MACs", s.UniqueDevices)
	}
	if len(c.Devices) != 3 {
		t.Errorf("got %d device records, want one per PHY/MAC pair (3)", len(c.Devices))
	}
}

func TestPacketCountUsesTheLargerSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.kismet")
	f := newFixture(t, path)
	f.device("Bluetooth", "AA:AA:AA:AA:AA:AA", 0, blob("Speaker", 568))
	f.device("IEEE802.11", "BB:BB:BB:BB:BB:BB", -40, blob("Xylo", 1))
	f.packets("IEEE802.11", "BB:BB:BB:BB:BB:BB", 1, 2, 3)

	c, err := Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got := find(t, c, "Bluetooth", "AA:AA:AA:AA:AA:AA").Packets; got != 568 {
		t.Errorf("device total not used: got %d, want 568", got)
	}
	if got := find(t, c, "IEEE802.11", "BB:BB:BB:BB:BB:BB").Packets; got != 3 {
		t.Errorf("packets table not used: got %d, want 3", got)
	}
}

func TestPacketCountsAreScopedToPHY(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.kismet")
	f := newFixture(t, path)
	f.device("Bluetooth", "AA:AA:AA:AA:AA:AA", 0, blob("", 0))
	f.device("BTLE", "AA:AA:AA:AA:AA:AA", -50, blob("", 0))
	f.packets("BTLE", "AA:AA:AA:AA:AA:AA", 1, 2)

	c, err := Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got := find(t, c, "Bluetooth", "AA:AA:AA:AA:AA:AA").Packets; got != 0 {
		t.Errorf("Bluetooth record got %d packets belonging to BTLE", got)
	}
}

func TestDeviceFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.kismet")
	f := newFixture(t, path)
	f.device("IEEE802.11", "AA:AA:AA:AA:AA:AA", 0, blob("AA:AA:AA:AA:AA:AA", 1))
	f.device("IEEE802.11", "BB:BB:BB:BB:BB:BB", -71, blob("Home", 1))
	f.device("IEEE802.11", "CC:CC:CC:CC:CC:CC", -60, []byte("{not json"))

	c, err := Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	unnamed := find(t, c, "IEEE802.11", "AA:AA:AA:AA:AA:AA")
	if unnamed.Name != "" {
		t.Errorf("MAC used as name: %q", unnamed.Name)
	}
	if unnamed.Signal != nil {
		t.Errorf("signal 0 should mean unmeasured, got %d", *unnamed.Signal)
	}
	named := find(t, c, "IEEE802.11", "BB:BB:BB:BB:BB:BB")
	if named.Name != "Home" || named.Signal == nil || *named.Signal != -71 {
		t.Errorf("named device = %+v", named)
	}
	if corrupt := find(t, c, "IEEE802.11", "CC:CC:CC:CC:CC:CC"); corrupt.Name != "" {
		t.Errorf("corrupt blob device = %+v", corrupt)
	}
}

func TestEmptyCapture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.kismet")
	newFixture(t, path)

	c, err := Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Summary.Start != nil || c.Summary.End != nil || c.Summary.PacketCount != 0 {
		t.Errorf("summary = %+v", c.Summary)
	}
}

func TestLoadDoesNotTouchTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.kismet")
	f := newFixture(t, path)
	f.device("IEEE802.11", "AA:AA:AA:AA:AA:AA", -40, blob("Home", 1))
	f.db.Close()

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if _, err := Load(context.Background(), path); err != nil {
		t.Fatalf("read-only file in read-only directory: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Error("capture was modified")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("sidecar files created: %v", entries)
	}
}

func TestLoadUnusualPaths(t *testing.T) {
	for _, name := range []string{"with space.kismet", "hash#1.kismet", "what?.kismet", "100%.kismet"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			newFixture(t, "file:"+strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path))
			if _, err := Load(context.Background(), path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.kismet")
	if err := os.WriteFile(garbage, []byte("not a database, just some text padding"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "foreign.kismet")
	db, err := sql.Open("sqlite", foreign)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE other (id INT)"); err != nil {
		t.Fatal(err)
	}

	tests := []struct{ name, path, want string }{
		{"missing", filepath.Join(dir, "nope.kismet"), "no such file"},
		{"directory", dir, "is a directory"},
		{"garbage", garbage, "not a Kismet database"},
		{"other sqlite", foreign, "not a Kismet database"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(context.Background(), tt.path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Home WiFi", "Home WiFi"},
		{"Café ☕ 日本語", "Café ☕ 日本語"},
		{"evil\x1b[2Jssid", `evil\u001b[2Jssid`},
		{"bell\x07", `bell\u0007`},
		{"c1\u009bcode", `c1\u009bcode`},
		{"line\nbreak", `line\u000abreak`},
		{"txt\u202Egnp", "txt\\u202egnp"},
		{"bad\xffbyte", "bad�byte"},
	}
	for _, tt := range tests {
		if got := sanitize(tt.in); got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHostileNameIsSanitizedOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.kismet")
	f := newFixture(t, path)
	f.device("IEEE802.11", "AA:AA:AA:AA:AA:AA", -40, blob("x\x1b]0;pwned\x07", 1))

	c, err := Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if name := c.Devices[0].Name; strings.ContainsAny(name, "\x1b\x07") {
		t.Errorf("control characters survived: %q", name)
	}
}
