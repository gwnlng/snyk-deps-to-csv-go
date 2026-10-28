package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/gwnlng/snyk-deps-to-csv-go/internal/client"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// OrgResult is the outcome of exporting one organization.
type OrgResult struct {
	Org  Org
	Path string
	Rows int
	Err  error
}

// ExportGroup lists the organizations in groupID and exports each one to
// <dir>/dependencies_<orgID>.csv. All organizations share c's rate limiters.
// A failure for one organization does not stop the others; the returned error
// joins every per-organization failure.
func ExportGroup(ctx context.Context, c *client.Client, groupID, dir string, logf func(string, ...any)) ([]OrgResult, error) {
	orgs, err := ListGroupOrgs(ctx, c, groupID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	logf("group %s: %d organization(s)", groupID, len(orgs))

	results := make([]OrgResult, 0, len(orgs))
	var errs []error
	for i, o := range orgs {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		res := OrgResult{Org: o}
		if !safeID.MatchString(o.ID) {
			res.Err = fmt.Errorf("org %q: unsafe organization ID, skipped", o.ID)
		} else {
			res.Path = filepath.Join(dir, "dependencies_"+o.ID+".csv")
			logf("org %d/%d: %s (%s)", i+1, len(orgs), o.ID, o.Name)
			res.Rows, res.Err = client.ExportToFile(ctx, c.ForOrg(o.ID), res.Path, logf)
			if res.Err != nil {
				res.Path = ""
				res.Err = fmt.Errorf("org %s: %w", o.ID, res.Err)
			}
		}
		if res.Err != nil {
			errs = append(errs, res.Err)
		}
		results = append(results, res)
	}
	return results, errors.Join(errs...)
}
