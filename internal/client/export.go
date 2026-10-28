package client

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var csvHeader = []string{
	"id", "name", "version", "latestVersion", "type", "isDeprecated",
	"issuesCritical", "issuesHigh", "issuesMedium", "issuesLow", "licenses", "projects",
}

func toRow(d Dependency) []string {
	lic := make([]string, len(d.Licenses))
	for i, l := range d.Licenses {
		lic[i] = l.ID
	}
	prj := make([]string, len(d.Projects))
	for i, p := range d.Projects {
		prj[i] = fmt.Sprintf("%s (%s)", p.Name, p.ID)
	}
	return []string{
		d.ID, d.Name, d.Version, d.LatestVersion, d.Type, strconv.FormatBool(d.IsDeprecated),
		strconv.Itoa(d.IssuesCritical), strconv.Itoa(d.IssuesHigh),
		strconv.Itoa(d.IssuesMedium), strconv.Itoa(d.IssuesLow),
		strings.Join(lic, ";"), strings.Join(prj, ";"),
	}
}

// Export pages through every result and writes CSV to w. It returns an error
// unless the number of rows written equals the total reported by the API.
func Export(ctx context.Context, c *Client, w io.Writer, logf func(string, ...any)) (int, error) {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return 0, err
	}
	written, total, pages := 0, -1, 0
	for page := 1; ; page++ {
		p, err := c.FetchPage(ctx, page)
		if err != nil {
			return written, err
		}
		total = p.Total
		if pages == 0 {
			pages = (total + c.perPage - 1) / c.perPage
		}
		for _, d := range p.Results {
			if err := cw.Write(toRow(d)); err != nil {
				return written, err
			}
			written++
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return written, err
		}
		logf("page %d/%d fetched (%d/%d dependencies)", page, pages, written, total)
		if len(p.Results) == 0 || written >= total {
			break
		}
		if page > pages+5 { // total drifted wildly; refuse to loop forever
			return written, fmt.Errorf("pagination did not converge after %d pages (%d/%d)", page, written, total)
		}
	}
	if written != total {
		return written, fmt.Errorf("incomplete export: wrote %d dependencies but API reported total %d", written, total)
	}
	return written, nil
}

// ExportToFile writes to a temp file and renames on success so a partial
// export never masquerades as a complete one.
func ExportToFile(ctx context.Context, c *Client, path string, logf func(string, ...any)) (int, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".deps-*.csv.tmp")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name()) // no-op after successful rename
	n, err := Export(ctx, c, tmp, logf)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	return n, os.Rename(tmp.Name(), path)
}
