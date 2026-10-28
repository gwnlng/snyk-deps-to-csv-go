# snyk-deps-to-csv-go

Exports all dependencies from the Snyk V1 [List all dependencies](https://docs.snyk.io/developer-tools/snyk-api/reference/dependencies-v1) API (`POST /v1/org/{orgId}/dependencies`) to a CSV file.

- Paginates until the last page and verifies that the number of rows written equals the `total` reported by the API. If not, it exits non-zero and no output file is produced.
- Filters: languages, projects, dependencies.
- Rate limiting for Snyk's tiered limits (150 requests/min, 1500 requests/hour per token):
  - [`uber-go/ratelimit`](https://github.com/uber-go/ratelimit) paces requests at 120/min.
  - [`golang.org/x/time/rate`](https://pkg.go.dev/golang.org/x/time/rate) enforces the 1500/hour budget.
  - [`hashicorp/go-retryablehttp`](https://github.com/hashicorp/go-retryablehttp) retries HTTP 429 and 5xx with its default backoff (honours `Retry-After`), up to 10 retries.
  - Limits apply to every HTTP attempt, including retries.

## Requirements

- Go 1.27.2 or newer (the `go` directive in `go.mod`)
- A Snyk API token with access to the organization, and the organization ID (Snyk UI → Settings → General)

## For users: running the CLI

Build once, then run:

```sh
make build

export SNYK_TOKEN=<your-snyk-api-token>
./snyk-deps-to-csv-go --org <org-id> --output dependencies.csv
```

Or install to `$GOPATH/bin`:

```sh
go install ./cmd/snyk-deps-to-csv     # installs a binary named "snyk-deps-to-csv" (the directory name)
snyk-deps-to-csv --org <org-id>
```

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `--org` | `$SNYK_ORG_ID` | Snyk organization ID. Exactly one of `--org` / `--group` is required; they are mutually exclusive |
| `--group` | | Snyk group ID: export **every organization in the group**, one CSV per org |
| `--token` | `$SNYK_TOKEN` | API token (required). Prefer the env var to keep it out of shell history |
| `--languages` | | Filter: comma-separated languages, e.g. `npm,maven` |
| `--projects` | | Filter: comma-separated project IDs |
| `--dependencies` | | Filter: comma-separated dependencies, e.g. `lodash@4.17.21` |
| `--per-page` | `1000` | Page size (1–1000) |
| `--output` | `dependencies.csv` | Output CSV path (`--org` mode only) |
| `--output-dir` | `.` | Directory for the per-org CSV files (`--group` mode only) |
| `--licenses` | | Filter: comma-separated licenses, e.g. `MIT,Apache-2.0` |
| `--severity` | | Filter: comma-separated severities; one of `critical`, `high`, `medium`, `low` (invalid values are rejected, exit code 2) |
| `--base-url` | `https://api.snyk.io` | API host (e.g. a regional host). `/v1` is appended automatically |

### Examples

```sh
# Everything in an org
SNYK_TOKEN=... ./snyk-deps-to-csv-go --org 1111-2222

# Only certain language dependencies, from two projects
./snyk-deps-to-csv-go --org 1111-2222 --languages java,python \
  --projects <project-id-1>,<project-id-2> --output npm-maven.csv

# Using the org env var
SNYK_ORG_ID=1111-2222 ./snyk-deps-to-csv-go --dependencies lodash@4.17.21

# Every organization in a group -> out/dependencies_<orgID>.csv for each org
./snyk-deps-to-csv-go --group <group-id> --output-dir out

# Only critical/high severity issues in MIT-licensed dependencies
./snyk-deps-to-csv-go --org 1111-2222 --severity critical,high --licenses MIT
```

Progress is logged to stderr (`page 3/12 fetched (3000/11432 dependencies)`). Flags use double dashes (`--org`); list flags accept comma-separated values and/or can be repeated. Exit codes: `0` success, `1` export failed or incomplete, `2` bad arguments. Ctrl-C cancels cleanly.

### Group mode

`--group` first calls the Snyk REST API "list all organizations in group" (`GET /rest/groups/{group_id}/orgs`, following `links.next` pagination), then exports each organization sequentially to `<output-dir>/dependencies_<orgID>.csv`. The same filters apply to every organization.

- The 150/min and 1500/hour limits are per token, so all organizations share one rate limiter (and one retry client).
- If one organization fails, the rest still run; a summary is logged and the exit code is `1`. No partial CSV is left for a failed organization.
- `--output` can't be combined with `--group`, and `--output-dir` can't be combined with `--org` (exit code `2`). If `SNYK_ORG_ID` is set in the environment, an explicit `--group` takes precedence.
- Your token needs access to the group. The REST API version is the `restVersion` constant in `internal/model/orgs.go`.

### Filters
See following language filters:
```
"cpp",
"dockerfile",
"dotnet",
"elixir",
"golang",
"helm",
"java",
"javascript",
"kubernetes",
"linux",
"php",
"python",
"ruby",
"scala",
"swift",
"terraform"
```

Valid `--severity` values: `critical`, `high`, `medium`, `low`.

### Output columns

`id, name, version, latestVersion, type, isDeprecated, issuesCritical, issuesHigh, issuesMedium, issuesLow, licenses, projects`

`licenses` is `;`-joined; `projects` is `name (id)` entries joined with `;`.

## For developers: development build

```sh
git clone <repo> && cd snyk-deps-to-csv-go
go mod download

go run ./cmd/snyk --org <org-id>          # run without building (needs SNYK_TOKEN)
go build ./...                  # compile check
go vet ./...
go test ./...                   # httptest-based; no network or token needed
```

### Layout

| File | Purpose |
|------|---------|
| `cmd/snyk/main.go` | Flag parsing, wiring, exit codes |
| `internal/client/client.go` | API client built on `retryablehttp`; `FetchPage` |
| `internal/client/limiter.go` | `http.RoundTripper` applying the uber and x/time limiters per attempt |
| `internal/client/export.go` | Pagination loop, completeness check, CSV writer (temp file + atomic rename) |
| `internal/client/*_test.go` | Pagination, 429 retry, completeness and rate-limit tier tests |
| `internal/model/orgs.go` | `ListGroupOrgs`: REST "list all organizations in group" with `links.next` pagination |
| `internal/model/group.go` | `ExportGroup`: one CSV per organization, sharing one client and rate limiter |
| `internal/model/*_test.go` | Group listing and per-org export tests |
| `cmd/snyk/main_test.go` | CLI flag/severity validation tests |

### Testing against a mock server

Point `--base-url` at any server implementing the endpoint, e.g. `--base-url http://localhost:8080`. The tests in `internal/client/export_test.go` show a minimal handler.

### Notes

- Snyk V1 is slated for eventual deprecation in favour of the REST API.
- The rate limit constants (`PaceRequestsPerMinute`, `HourlyBudget`) live in `internal/client/limiter.go`. The limits are per token, so other tools using the same token share the budget.
