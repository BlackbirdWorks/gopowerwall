---
name: upstream-sync
description: >-
  Use this skill to audit, track, and catch up with changes, bug fixes, and releases
  from the upstream Python pypowerwall repository (jasonacox/pypowerwall).
---

# Upstream Catch-Up Guide (pypowerwall -> gopowerwall)

`gopowerwall` is a Go port of [jasonacox/pypowerwall](https://github.com/jasonacox/pypowerwall).
This skill provides the end-to-end workflow for auditing upstream changes, updating tracked markers, implementing missing features or bugfixes while respecting the parity contract, and verifying compliance with repository standards.

---

## The Upstream Sync Lifecycle

Every upstream catch-up execution follows a strict 5-phase lifecycle:

```
[1. Diff & Inventory] ──> [2. Write docs/upstream-sync-audit.md]
                                       │
                                       ▼
[4. Update Markers & README] <── [3. Port Code & Add Table Tests]
             │
             ▼
[5. Verify (lint/test/cov) & Delete docs/upstream-sync-audit.md]
```

1. **Diff & Inventory**: Use GitHub compare API to discover all commits and modified files between the currently tracked commit and target upstream commit.
2. **Write Working Audit**: Generate [`docs/upstream-sync-audit.md`](file:///Users/andrew.bishop/Documents/Code/gopowerwall/docs/upstream-sync-audit.md) as a temporary checklist detailing every commit, changed file, and porting requirements.
3. **Port Code & Write Table Tests**: Implement the changes following idiomatic Go and parity rules, with table tests maintaining >= 85% coverage.
4. **Update Version Markers**: Advance `UpstreamVersion`, `UpstreamCommit`, and `Build` in [`pkgs/version/version.go`](file:///Users/andrew.bishop/Documents/Code/gopowerwall/pkgs/version/version.go) and update [`README.md`](file:///Users/andrew.bishop/Documents/Code/gopowerwall/README.md).
5. **Verify & Clean Up**: Run `make test`, `make lint`, and `make total-coverage`. Once fully verified and green, **delete** [`docs/upstream-sync-audit.md`](file:///Users/andrew.bishop/Documents/Code/gopowerwall/docs/upstream-sync-audit.md) so no transient audit files remain in the repository.

---

## 1. The Parity Contract

Parity with upstream Python is strictly required for two external surfaces:

1. **CLI (`cmd/gopowerwall`)**: subcommand names, flags, arguments, output formats (`text`, `json`, `csv`), and human-readable formatting.
2. **Proxy HTTP surface (`proxy`)**: route paths, HTTP verbs, status codes, JSON/CSV response shapes, headers, and frozen constants.

### Key Rules
- **Python is the authority on behavior**: When in doubt about endpoint semantics, default values, or edge cases, read upstream Python directly. Cite file and line numbers (e.g. `pypowerwall/tedapi/__init__.py:1234`) in commit messages and PR descriptions.
- **Do not replicate Python structure**: Internal Go package architecture, exported identifiers, types, and function signatures should follow idiomatic Go (see `.agent/rules/go-style-guide.md`). Do not create Python-mirroring wrapper shims.
- **Frozen constants**:
  - `version.Build` (e.g. `"t106"`, `"t108"`) is the pypowerwall proxy build tag reported by `/api/status` and `/stats`. It only changes when tracking an upstream proxy release.
  - Release versioning of `gopowerwall` is handled separately via `version.BuildVersion` injected by ldflags.

---

## 2. Upstream Tracking Markers

Upstream version tracking is codified in [`pkgs/version/version.go`](file:///Users/andrew.bishop/Documents/Code/gopowerwall/pkgs/version/version.go):

```go
const (
    // Build is the upstream proxy build tag (e.g. "t106", "t108").
    Build = "t106"

    // UpstreamVersion is the upstream pypowerwall release version tracked for parity.
    UpstreamVersion = "0.20.0"

    // UpstreamCommit is the full 40-character git commit hash of jasonacox/pypowerwall.
    UpstreamCommit = "6c7fdbe361aee43fde0bed6506d12700680c714c"
)
```

Whenever catching up from upstream:
1. Record the upstream git commit SHA and release tag.
2. Update `UpstreamVersion`, `UpstreamCommit`, and `Build` in `pkgs/version/version.go`.
3. Update corresponding unit tests in `pkgs/version/version_test.go` and `proxy/handlers_test.go`.

---

## 3. Auditing Upstream Changes

Use the GitHub CLI (`gh`) to compare the currently tracked commit against upstream `main` or a target tag:

### Step 1: List Upstream Commits
```bash
gh api 'repos/jasonacox/pypowerwall/compare/<UpstreamCommit>...main' \
  --jq '{total: .total_commits, commits: [.commits[] | "\(.sha[0:10]) \(.commit.author.date) \(.commit.message | split("\n")[0])"]}'
```

### Step 2: Identify Changed Files
Focus specifically on production logic, ignoring tools/packaging unless relevant:
```bash
gh api 'repos/jasonacox/pypowerwall/compare/<UpstreamCommit>...main' \
  --jq '.files[] | select(.filename | test("^(pypowerwall/|proxy/)") and (test("tests/") | not)) | "\(.status)\t\(.filename)"'
```

### Step 3: Inspect Upstream Diffs
Inspect file-specific diffs for exact behavior changes:
```bash
gh api 'repos/jasonacox/pypowerwall/compare/<UpstreamCommit>...main' \
  --jq '.files[] | select(.filename == "proxy/server.py") | .patch'
```

### Step 4: Generate Temporary Working Audit (`docs/upstream-sync-audit.md`)
To guarantee no commits or subtle behavioral changes are missed during porting:
1. Write [`docs/upstream-sync-audit.md`](file:///Users/andrew.bishop/Documents/Code/gopowerwall/docs/upstream-sync-audit.md) as a markdown checklist before making code changes.
2. The audit file must catalog:
   - **Metadata**: Base commit SHA & version, target commit SHA & version, and date.
   - **Commit Manifest**: Complete table of all commits between base and target (SHA, date, subject message, and impact classification).
   - **Upstream File Diff & Porting Map**: Detailed table mapping each changed upstream file to its Go implementation target, status (`Ported`, `Verified`, `N/A`), and concrete implementation details.
   - **Verification Matrix**: Table of test files added/updated, test outcomes, linter results, and statement coverage percentage.
3. Use the checklist to guide and track implementation in step 4 of the lifecycle.
4. Once implementation, tests, linting, and coverage are fully green and version markers are updated, **delete** `docs/upstream-sync-audit.md`.

### Key Areas to Audit

| Area | Upstream Files | What to Look For |
|---|---|---|
| **Proxy API & Routes** | `proxy/server.py` | New endpoints, changes to allowlists, field masking on unauthenticated routes (`/stats`, `/help`, `/`), new environment variables (e.g. `PW_FAILOVER`). |
| **Powerwall Facade** | `pypowerwall/__init__.py` | New methods (`get_tariff`, `set_tariff`), connect fallback & retry loops, changes to default values or initialization arguments. |
| **TEDAPI & Vitals** | `pypowerwall/tedapi/__init__.py`, `pypowerwall/tedapi/pypowerwall_tedapi.py`, `pypowerwall/tedapi/queries/` | Extra signal names appended to queries, hardware-specific vitals mappings (PW2 vs PW3), remote meter handling (`trm_mb`, `trm_wifi`), failover transitions. |
| **Cloud / FleetAPI** | `pypowerwall/cloud/pypowerwall_cloud.py`, `pypowerwall/fleetapi/fleetapi.py` | Re-auth logic, token hygiene, site recovery on 404, tariff endpoints. |
| **Protobuf Definitions** | `pypowerwall/tedapi/protobuf/` | Updates to `.proto` schemas. If schemas change, update `powerwall/proto/` and run `make proto`. |

---

## 4. Implementation Guidelines

1. **Keep Types Strongly Typed**:
   - Define domain structures under [`models/`](file:///Users/andrew.bishop/Documents/Code/gopowerwall/models) with explicit struct tags (`json:"..."`).
   - Avoid generic `map[string]any` wherever contract shapes are known.
2. **Context and Logging**:
   - Always pass `context.Context` through operation and client calls.
   - Extract `*slog.Logger` from the context via `logger.FromContext(ctx)`. Never use global loggers.
3. **Avoid Complex Branching & Returns**:
   - Keep the happy path left-aligned. Return early. Avoid `else` blocks and `break` statements.
4. **Sentinel Errors**:
   - Define exported sentinel errors (e.g. `ErrInvalidParameter = errors.New(...)`) in package `errors.go` or package root.
   - Check errors with `errors.Is()` or `errors.As()`.
5. **Protobuf Regeneration**:
   - If upstream protobuf definitions change, never hand-edit the `.pb.go` files.
   - Place `.proto` files in `powerwall/proto/` and run `make proto`. Ensure output matches `protoc-gen-go v1.36.12`.

---

## 5. Testing and Quality Verification

All additions and modifications MUST strictly adhere to repository testing rules:

- **Table tests are mandatory**: Every test function must be written as a table test iterating over test cases with descriptive `name` fields.
- **Concurrency**: Mark tests and subtests with `t.Parallel()` unless an environment variable or package-level global is mutated.
- **Assertions**: Always use `github.com/stretchr/testify/assert` and `github.com/stretchr/testify/require`. Never use `t.Fatal`, `t.Fatalf`, `t.Error`, or `t.Errorf`.
- **Test Context**: Use `t.Context()` when creating contexts in tests.
- **Coverage**: Maintain at least 85% coverage across packages (`make total-coverage`).
- **No Linter Waivers**: Do not use `//nolint` or disable linter rules without explicit necessity and justification.

### Verification Commands
```bash
# Run unit tests
make test

# Run integration tests (requires docker/testcontainers)
make integration-test

# Verify linting and vulnerabilities
make lint

# Check combined code coverage
make total-coverage
```
