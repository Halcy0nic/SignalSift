// Package kismet reads Kismet capture databases (.kismet files, which are
// SQLite) without modifying them.
package kismet

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sync/errgroup"
	"modernc.org/sqlite" // Also registers the pure-Go "sqlite" driver.
	sqlite3 "modernc.org/sqlite/lib"
)

// Device is one row of the Kismet devices table. The same MAC can produce
// several Devices when it was seen on more than one PHY.
type Device struct {
	MAC          string    `json:"mac"`
	Name         string    `json:"name"`
	PHY          string    `json:"phy"`
	Type         string    `json:"type"`
	Manufacturer string    `json:"manufacturer"`
	Channel      string    `json:"channel"`
	Encryption   string    `json:"encryption"`
	Signal       *int      `json:"signal"` // Strongest dBm; nil when never measured.
	Packets      int64     `json:"packets"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
}

// Summary describes a capture as a whole.
type Summary struct {
	File            string     `json:"file"`
	KismetVersion   string     `json:"kismet_version"`
	Start           *time.Time `json:"start"` // nil when the capture has no packets.
	End             *time.Time `json:"end"`
	DurationSeconds int64      `json:"duration_seconds"`
	UniqueDevices   int        `json:"unique_devices"`
	PacketCount     int64      `json:"packet_count"`
}

// Capture is a fully loaded Kismet database.
type Capture struct {
	Summary Summary
	Devices []Device
}

// Load opens the database at path read-only and reads its summary and devices.
func Load(ctx context.Context, path string) (*Capture, error) {
	if info, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("cannot read capture: %w", err)
	} else if info.IsDir() {
		return nil, fmt.Errorf("cannot read capture: %s is a directory", path)
	}
	// SQLite reports an unhelpful error for unreadable files, so check first.
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read capture: %w", err)
	}
	f.Close()

	dsn, err := readOnlyDSN(path)
	if err != nil {
		return nil, err
	}
	capture, err := read(ctx, path, dsn)
	if needsRollback(err) {
		return readRecovered(ctx, path)
	}
	return capture, err
}

// needsRollback reports whether SQLite refused a read-only open because the
// capture has an interrupted write that must be rolled back first.
func needsRollback(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_READONLY
}

// readRecovered handles a capture left mid-write, for example by a Kismet crash.
// Rolling back modifies the database, so it happens on a private temporary copy
// and the original is never written.
func readRecovered(ctx context.Context, path string) (*Capture, error) {
	dir, err := os.MkdirTemp("", "signalsift-")
	if err != nil {
		return nil, fmt.Errorf("recover %s: %w", path, err)
	}
	defer os.RemoveAll(dir)

	recovered := filepath.Join(dir, "capture.kismet")
	for _, suffix := range []string{"", "-journal"} {
		if err := copyFile(path+suffix, recovered+suffix); err != nil {
			return nil, fmt.Errorf("recover %s: %w", path, err)
		}
	}
	return read(ctx, path, (&url.URL{Scheme: "file", Path: recovered}).String())
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// read loads a capture through dsn, reporting it under its original path.
func read(ctx context.Context, path, dsn string) (*Capture, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer db.Close()

	summary := Summary{File: path}
	err = db.QueryRowContext(ctx, "SELECT kismet_version FROM KISMET LIMIT 1").Scan(&summary.KismetVersion)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s is not a Kismet database: %w", path, err)
	}

	var counts map[packetKey]int64
	var g errgroup.Group
	g.Go(func() error { return readSummary(ctx, db, &summary) })
	g.Go(func() (err error) {
		counts, err = readPacketCounts(ctx, db)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	devices, err := readDevices(ctx, db, counts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &Capture{Summary: summary, Devices: devices}, nil
}

// readOnlyDSN builds a SQLite URI for path. Finished captures are opened as
// immutable so read-only media works and no journal files are created. A
// sibling journal means Kismet may still be writing, so immutable is dropped
// to let SQLite honour its locks.
func readOnlyDSN(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	query := "mode=ro&immutable=1"
	for _, suffix := range []string{"-journal", "-wal"} {
		if _, err := os.Stat(abs + suffix); err == nil {
			query = "mode=ro"
		}
	}
	return (&url.URL{Scheme: "file", Path: abs, RawQuery: query}).String(), nil
}

func readSummary(ctx context.Context, db *sql.DB, s *Summary) error {
	var start, end sql.NullInt64
	err := db.QueryRowContext(ctx,
		"SELECT MIN(ts_sec), MAX(ts_sec), COUNT(*) FROM packets").Scan(&start, &end, &s.PacketCount)
	if err != nil {
		return fmt.Errorf("read packets: %w", err)
	}
	// Bluetooth sources log devices but no packets, so a Bluetooth-only capture
	// takes its time range from when its devices were seen.
	if !start.Valid {
		err = db.QueryRowContext(ctx, `
			SELECT MIN(first_time), MAX(last_time) FROM devices
			WHERE first_time > 0 AND last_time > 0`).Scan(&start, &end)
		if err != nil {
			return fmt.Errorf("read devices: %w", err)
		}
	}
	if start.Valid && end.Valid {
		first, last := time.Unix(start.Int64, 0), time.Unix(end.Int64, 0)
		s.Start, s.End = &first, &last
		s.DurationSeconds = end.Int64 - start.Int64
	}
	err = db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT devmac) FROM devices").Scan(&s.UniqueDevices)
	if err != nil {
		return fmt.Errorf("read devices: %w", err)
	}
	return nil
}

type packetKey struct{ phy, mac string }

func readPacketCounts(ctx context.Context, db *sql.DB) (map[packetKey]int64, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT phyname, sourcemac, COUNT(*) FROM packets GROUP BY phyname, sourcemac")
	if err != nil {
		return nil, fmt.Errorf("count packets: %w", err)
	}
	defer rows.Close()

	counts := make(map[packetKey]int64)
	for rows.Next() {
		var phy, mac sql.NullString
		var n int64
		if err := rows.Scan(&phy, &mac, &n); err != nil {
			return nil, fmt.Errorf("count packets: %w", err)
		}
		counts[packetKey{phy.String, mac.String}] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("count packets: %w", err)
	}
	return counts, nil
}

// deviceRecord holds the fields used from the JSON blob in devices.device.
type deviceRecord struct {
	CommonName   string `json:"kismet.device.base.commonname"`
	Manufacturer string `json:"kismet.device.base.manuf"`
	Channel      string `json:"kismet.device.base.channel"`
	Encryption   string `json:"kismet.device.base.crypt"`
	Packets      int64  `json:"kismet.device.base.packets.total"`
}

func readDevices(ctx context.Context, db *sql.DB, counts map[packetKey]int64) ([]Device, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT devmac, phyname, type, strongest_signal, first_time, last_time, device
		FROM devices`)
	if err != nil {
		return nil, fmt.Errorf("read devices: %w", err)
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		var mac, phy, kind sql.NullString
		var signal, first, last sql.NullInt64
		var blob []byte
		if err := rows.Scan(&mac, &phy, &kind, &signal, &first, &last, &blob); err != nil {
			return nil, fmt.Errorf("read devices: %w", err)
		}

		// A malformed blob only costs the optional fields, so keep the device.
		var rec deviceRecord
		_ = json.Unmarshal(blob, &rec)

		d := Device{
			MAC:          mac.String,
			PHY:          sanitize(phy.String),
			Type:         sanitize(kind.String),
			Manufacturer: sanitize(rec.Manufacturer),
			Channel:      sanitize(rec.Channel),
			Encryption:   sanitize(rec.Encryption),
			FirstSeen:    time.Unix(first.Int64, 0),
			LastSeen:     time.Unix(last.Int64, 0),
		}
		// Kismet falls back to the MAC when a device has no name.
		if !strings.EqualFold(rec.CommonName, d.MAC) {
			d.Name = sanitize(rec.CommonName)
		}
		// Kismet stores 0 when it never measured a signal.
		if signal.Valid && signal.Int64 != 0 {
			v := int(signal.Int64)
			d.Signal = &v
		}
		// Neither source is complete: the packets table has nothing under
		// Bluetooth PHYs, and the device total can lag the packets table.
		d.Packets = max(rec.Packets, counts[packetKey{phy.String, mac.String}])
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read devices: %w", err)
	}
	return devices, nil
}

// sanitize makes over-the-air text safe to print. SSIDs and Bluetooth names are
// chosen by whoever is transmitting, so control characters (terminal escape
// sequences) and bidirectional overrides (text spoofing) are shown as visible
// escapes instead of being passed through.
func sanitize(s string) string {
	if !strings.ContainsFunc(s, isUnsafe) && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(s, "�") {
		if isUnsafe(r) {
			fmt.Fprintf(&b, `\u%04x`, r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isUnsafe(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) ||
		r == ' ' || r == ' '
}

// LoadPair loads two captures concurrently.
func LoadPair(ctx context.Context, baselinePath, followupPath string) (baseline, followup *Capture, err error) {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		baseline, err = Load(ctx, baselinePath)
		return err
	})
	g.Go(func() (err error) {
		followup, err = Load(ctx, followupPath)
		return err
	})
	if err := g.Wait(); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() == nil {
			return nil, nil, fmt.Errorf("interrupted: %w", err)
		}
		return nil, nil, err
	}
	return baseline, followup, nil
}
