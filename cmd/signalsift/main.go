// Command signalsift compares Kismet wireless captures from the terminal.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/Halcy0nic/SignalSift/internal/compare"
	"github.com/Halcy0nic/SignalSift/internal/kismet"
	"github.com/Halcy0nic/SignalSift/internal/render"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		stop()
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "signalsift",
		Short:         "Compare Kismet wireless captures",
		Long:          "SignalSift compares a baseline Kismet capture to a follow-up capture and\nreports which devices are new, which are missing, and which are most active.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newCompareCmd(), newReportCmd())
	return root
}

// filterFlags are the options shared by every command that builds a report.
type filterFlags struct {
	top        int
	minPackets int64
}

func (f *filterFlags) register(cmd *cobra.Command) {
	cmd.Flags().IntVar(&f.top, "top", 10, "most active devices to list per capture (0 hides these lists)")
	cmd.Flags().Int64Var(&f.minPackets, "min-packets", 0, "leave out devices with fewer packets than this")
}

func (f *filterFlags) validate() error {
	if f.top < 0 {
		return errors.New("--top cannot be negative")
	}
	if f.minPackets < 0 {
		return errors.New("--min-packets cannot be negative")
	}
	return nil
}

func (f *filterFlags) build(ctx context.Context, baselinePath, followupPath string) (compare.Report, error) {
	baseline, followup, err := kismet.LoadPair(ctx, baselinePath, followupPath)
	if err != nil {
		return compare.Report{}, err
	}
	return compare.Build(baseline, followup, compare.Options{
		Top:        f.top,
		MinPackets: f.minPackets,
	}), nil
}

func newCompareCmd() *cobra.Command {
	var (
		filters  filterFlags
		format   string
		htmlPath string
	)
	cmd := &cobra.Command{
		Use:   "compare <baseline.kismet> <followup.kismet>",
		Short: "Compare two captures and print the differences",
		Example: `  signalsift compare baseline.kismet followup.kismet
  signalsift compare baseline.kismet followup.kismet --top 5 --min-packets 10
  signalsift compare baseline.kismet followup.kismet --format json
  signalsift compare baseline.kismet followup.kismet --format csv > devices.csv
  signalsift compare baseline.kismet followup.kismet --html report.html`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := filters.validate(); err != nil {
				return err
			}
			if format != "table" && format != "json" && format != "csv" {
				return fmt.Errorf("unknown format %q (want table, json or csv)", format)
			}
			report, err := filters.build(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			if err := render.Write(cmd.OutOrStdout(), format, report); err != nil {
				return err
			}
			if htmlPath != "" {
				return writeHTML(cmd.ErrOrStderr(), htmlPath, report)
			}
			return nil
		},
	}
	filters.register(cmd)
	cmd.Flags().StringVarP(&format, "format", "f", "table", "output format: table, json or csv")
	cmd.Flags().StringVar(&htmlPath, "html", "", "also write a self-contained HTML report to this path")
	return cmd
}

func newReportCmd() *cobra.Command {
	var (
		filters filterFlags
		output  string
	)
	cmd := &cobra.Command{
		Use:     "report <baseline.kismet> <followup.kismet>",
		Short:   "Write a self-contained HTML report",
		Example: `  signalsift report baseline.kismet followup.kismet -o report.html`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := filters.validate(); err != nil {
				return err
			}
			report, err := filters.build(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return writeHTML(cmd.ErrOrStderr(), output, report)
		},
	}
	filters.register(cmd)
	cmd.Flags().StringVarP(&output, "output", "o", "report.html", "path of the HTML report")
	return cmd
}

// writeHTML saves the report owner-readable only, since it describes the
// devices at a surveyed location.
func writeHTML(status io.Writer, path string, report compare.Report) error {
	var buf bytes.Buffer
	if err := render.HTML(&buf, report, time.Now()); err != nil {
		return fmt.Errorf("render report: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	fmt.Fprintln(status, "wrote", path)
	return nil
}
