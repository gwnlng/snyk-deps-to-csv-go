package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestSeverityFlag(t *testing.T) {
	cases := []struct {
		args    []string
		want    []string
		wantErr bool
	}{
		{[]string{"--severity", "high"}, []string{"high"}, false},
		{[]string{"--severity", "critical,high"}, []string{"critical", "high"}, false},
		{[]string{"--severity", "low", "--severity", "medium"}, []string{"low", "medium"}, false},
		{nil, nil, false},
		{[]string{"--severity", "urgent"}, nil, true},
		{[]string{"--severity", "high,bogus"}, nil, true},
		{[]string{"--severity", "HIGH"}, nil, true},
	}
	for _, c := range cases {
		cmd := newRootCmd()
		cmd.RunE = func(*cobra.Command, []string) error { return nil } // parse only
		cmd.SetArgs(c.args)
		err := cmd.Execute()
		if c.wantErr {
			var ue usageError
			if !errors.As(err, &ue) {
				t.Errorf("%v: want usageError, got %v", c.args, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v: unexpected error %v", c.args, err)
			continue
		}
		got := cmd.Flags().Lookup("severity").Value.String()
		if want := joinCSV(c.want); got != want {
			t.Errorf("%v: severity=%q want %q", c.args, got, want)
		}
	}
}

func joinCSV(v []string) string {
	return strings.Join(v, ",")
}

func TestOrgGroupFlagMatrix(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		args    []string
		wantErr bool
	}{
		{"org only", "", []string{"--org", "o1"}, false},
		{"group only", "", []string{"--group", "g1"}, false},
		{"both explicit", "", []string{"--org", "o1", "--group", "g1"}, true},
		{"neither", "", nil, true},
		{"env org only", "o-env", nil, false},
		{"env org overridden by group", "o-env", []string{"--group", "g1"}, false},
		{"group with --output", "", []string{"--group", "g1", "--output", "x.csv"}, true},
		{"org with --output-dir", "", []string{"--org", "o1", "--output-dir", "d"}, true},
		{"group with --output-dir", "", []string{"--group", "g1", "--output-dir", "d"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("SNYK_ORG_ID", c.env)
			t.Setenv("SNYK_TOKEN", "tok")
			cmd := newRootCmdWithRunner(func(*options, *cobra.Command) error { return nil }) // validation only
			cmd.SetArgs(c.args)
			err := cmd.Execute()
			var ue usageError
			if c.wantErr != errors.As(err, &ue) || (!c.wantErr && err != nil) {
				t.Fatalf("wantErr=%v err=%v", c.wantErr, err)
			}
		})
	}
}
