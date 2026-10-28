package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	"github.com/gwnlng/snyk-deps-to-csv-go/internal/client"
	"github.com/gwnlng/snyk-deps-to-csv-go/internal/model"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var validSeverities = []string{"critical", "high", "medium", "low"}

// severityValue is a pflag.Value that accepts a comma-separated and/or
// repeated list, rejecting anything outside validSeverities at parse time.
type severityValue struct{ vals *[]string }

func (s severityValue) String() string {
	if s.vals == nil {
		return ""
	}
	return strings.Join(*s.vals, ",")
}

func (s severityValue) Set(in string) error {
	for _, v := range strings.Split(in, ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		ok := false
		for _, valid := range validSeverities {
			if v == valid {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("invalid severity %q: must be one of %s", v, strings.Join(validSeverities, ", "))
		}
		*s.vals = append(*s.vals, v)
	}
	return nil
}

func (s severityValue) Type() string { return "severities" }

var _ pflag.Value = severityValue{}

// usageError marks errors caused by bad arguments (exit code 2).
type usageError struct{ error }

type options struct {
	org, group, token, output, outputDir, baseURL string
	perPage                                       int
	filters                                       client.Filters
}

// validate enforces that exactly one of --org / --group selects the mode.
// --org defaults from $SNYK_ORG_ID, so only explicitly set flags can conflict;
// an explicit --group overrides an org that came only from the environment.
func (o *options) validate(flags *pflag.FlagSet) error {
	if flags.Changed("org") && flags.Changed("group") {
		return usageError{errors.New("--org and --group are mutually exclusive; provide only one")}
	}
	groupMode := o.group != ""
	if !groupMode && o.org == "" {
		return usageError{errors.New("one of --org (or SNYK_ORG_ID) or --group is required")}
	}
	if o.token == "" {
		return usageError{errors.New("a token is required (SNYK_TOKEN or --token)")}
	}
	if o.perPage < 1 || o.perPage > 1000 {
		return usageError{errors.New("--per-page must be 1..1000")}
	}
	if groupMode && flags.Changed("output") {
		return usageError{errors.New("--output applies to --org; use --output-dir with --group")}
	}
	if !groupMode && flags.Changed("output-dir") {
		return usageError{errors.New("--output-dir applies to --group; use --output with --org")}
	}
	return nil
}

func (o *options) exportOrg(cmd *cobra.Command) error {
	c := client.NewClient(o.baseURL, o.org, o.token, o.perPage, o.filters, client.PaceRequestsPerMinute, client.HourlyBudget)
	n, err := client.ExportToFile(cmd.Context(), c, o.output, log.Printf)
	if err != nil {
		return err
	}
	log.Printf("done: %d dependencies written to %s (all pages retrieved, matches API total)", n, o.output)
	return nil
}

func (o *options) exportGroup(cmd *cobra.Command) error {
	c := client.NewClient(o.baseURL, "", o.token, o.perPage, o.filters, client.PaceRequestsPerMinute, client.HourlyBudget)
	results, err := model.ExportGroup(cmd.Context(), c, o.group, o.outputDir, log.Printf)
	ok, failed := 0, 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			continue
		}
		ok++
		log.Printf("org %s (%s): %d dependencies -> %s", r.Org.ID, r.Org.Name, r.Rows, r.Path)
	}
	log.Printf("group %s summary: %d organization(s) exported, %d failed", o.group, ok, failed)
	return err
}

// run dispatches to group or single-org mode after validation succeeds.
func (o *options) run(cmd *cobra.Command) error {
	if o.group != "" {
		return o.exportGroup(cmd)
	}
	return o.exportOrg(cmd)
}

func newRootCmd() *cobra.Command {
	return newRootCmdWithRunner((*options).run)
}

// newRootCmdWithRunner lets tests replace the export step while keeping the
// real flag parsing and validation.
func newRootCmdWithRunner(runner func(*options, *cobra.Command) error) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:           "snyk-deps-to-csv-go",
		Short:         "Export Snyk V1 organization dependencies to CSV",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := o.validate(cmd.Flags()); err != nil {
				return err
			}
			return runner(o, cmd)
		},
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })

	f := cmd.Flags()
	f.StringVar(&o.org, "org", os.Getenv("SNYK_ORG_ID"), "Snyk organization ID (or SNYK_ORG_ID); mutually exclusive with --group")
	f.StringVar(&o.group, "group", "", "Snyk group ID: export every organization in the group (mutually exclusive with --org)")
	f.StringVar(&o.token, "token", os.Getenv("SNYK_TOKEN"), "Snyk API token (prefer SNYK_TOKEN env)")
	f.StringSliceVar(&o.filters.Languages, "languages", nil, "filter: comma-separated languages (e.g. npm,maven)")
	f.StringSliceVar(&o.filters.Projects, "projects", nil, "filter: comma-separated project IDs")
	f.StringSliceVar(&o.filters.Dependencies, "dependencies", nil, "filter: comma-separated dependencies (e.g. lodash@4.17.21)")
	f.StringSliceVar(&o.filters.Licenses, "licenses", nil, "filter: comma-separated licenses (e.g. MIT,Apache-2.0)")
	f.Var(severityValue{&o.filters.Severity}, "severity", "filter: comma-separated severities: "+strings.Join(validSeverities, ", "))
	f.IntVar(&o.perPage, "per-page", 1000, "page size (1..1000)")
	f.StringVar(&o.output, "output", "dependencies.csv", "output CSV path (--org mode)")
	f.StringVar(&o.outputDir, "output-dir", ".", "directory for dependencies_<orgID>.csv files (--group mode)")
	f.StringVar(&o.baseURL, "base-url", "https://api.snyk.io", "Snyk API host; /v1 is appended automatically")

	_ = cmd.RegisterFlagCompletionFunc("severity", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return validSeverities, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// run executes the CLI and returns the process exit code, so that deferred
// cleanup completes before main calls os.Exit.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err := newRootCmd().ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	var ue usageError
	if errors.As(err, &ue) {
		fmt.Fprintf(os.Stderr, "error: %v\n(see --help)\n", err)
		return 2
	}
	log.Printf("FAILED: %v", err)
	return 1
}

func main() {
	os.Exit(run())
}
