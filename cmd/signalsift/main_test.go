package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Halcy0nic/SignalSift/internal/compare"
)

func run(args ...string) (stdout, stderr string, err error) {
	var out, errBuf bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.ExecuteContext(context.Background())
	return out.String(), errBuf.String(), err
}

func TestArgumentValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"too few files", []string{"compare", "a"}, "accepts 2 arg"},
		{"bad format", []string{"compare", "a", "b", "--format", "xml"}, "unknown format"},
		{"negative top", []string{"compare", "a", "b", "--top", "-1"}, "--top cannot be negative"},
		{"negative min", []string{"report", "a", "b", "--min-packets", "-1"}, "--min-packets cannot be negative"},
		{"missing file", []string{"compare", "nope-a", "nope-b"}, "cannot read capture"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := run(tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestWriteHTMLFailureIsReported(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir", "r.html")
	var buf bytes.Buffer
	if err := writeHTML(&buf, missing, compare.Report{}); err == nil {
		t.Error("expected an error for an unwritable path")
	}
}

func TestWriteHTMLIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.html")
	var buf bytes.Buffer
	if err := writeHTML(&buf, path, compare.Report{}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
	if !strings.Contains(buf.String(), path) {
		t.Errorf("status = %q", buf.String())
	}
}
