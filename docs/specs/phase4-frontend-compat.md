# Phase 4 — Frontend Compatibility: Static JS Drop-in

## Status: Planning

## Context and motivation

The Go rewrite (`GOrg-CloudTools`) has a complete backend: models, DB, execution
primitives, change management, auth, AWS resource queries. The original Python/FastAPI
app (`~/git/CloudOpsTools`) has a working static-JS frontend (Bootstrap + vanilla JS,
Jinja2 server-side rendering) that operators already know.

Rather than build a TUI or a new web frontend, Phase 4 wires the existing static JS
frontend to the Go backend. This is the fastest path to having a fully operational Go
binary that management can evaluate, while keeping the option open to revisit the TUI
(Phase 2) or a proper SPA later.

**Goal:** a single Go binary that serves the HTML pages and satisfies every `fetch()`
call the existing JS makes — replacing the Python/uvicorn stack with no JS changes
(or only mechanical path updates).

---

## Source material

| Item | Path |
|---|---|
| Python backend | `~/git/CloudOpsTools/backend/` |
| HTML templates (Jinja2) | `~/git/CloudOpsTools/backend/templates/` |
| Static JS/CSS | `~/git/CloudOpsTools/backend/static/` |
| SQLite test DB | `~/git/CloudOpsTools/data/cloudopstools.db` |
| Go backend | `~/git/GOrg-CloudTools/` |
| Go API routes | `internal/api/server.go` |

---

## Architecture decision: cut server-side rendering

The Python app has two roles:

1. **SSR** — Jinja2 templates rendered per-request, injecting `current_change`,
   CSRF tokens, and session state into the HTML.
2. **JSON API** — endpoints the JS calls via `fetch()`.

We are not porting Jinja2 to Go templates. Instead, templates are **flattened to
static HTML** and all data loading moves to JS `fetch()` calls on `DOMContentLoaded`.
This is the right long-term direction regardless and removes the SSR dependency.

Consequences:
- Every piece of server-injected state (`current_change`, session info) must be
  fetchable via a JSON endpoint.
- Go serves the flattened HTML files as static assets alongside `/static/`.
- No template engine is needed in Go.

---

## Current Go backend route inventory

```
GET  /health
GET  /api/health

POST   /api/auth/aws-credentials
GET    /api/auth/aws-credentials/{environment}
DELETE /api/auth/aws-credentials/{environment}
GET    /api/auth/session-status
GET    /api/auth/aws-check-credentials

POST /api/exec/script
POST /api/exec/org-script
GET  /api/exec/jobs/{id}
POST /api/exec/jobs/{id}/resume
GET  /api/aws/ssm/commands/{command_id}/status

GET /api/aws/instances
GET /api/aws/vpcs
GET /api/aws/org/accounts

POST  /api/sessions
GET   /api/sessions/
GET   /api/sessions/{id}
PATCH /api/sessions/{id}/status

GET    /api/scripts/
GET    /api/scripts/{id}
POST   /api/scripts/
PATCH  /api/scripts/{id}
DELETE /api/scripts/{id}

GET   /api/changes/
GET   /api/changes/{id}
POST  /api/changes/
PATCH /api/changes/{id}

GET  /api/tools/
GET  /api/tools/{id}
POST /api/exec/tool

/static/*  (static file server, configurable root)
```

---

## Python frontend endpoint inventory

All `fetch()` calls extracted from `backend/static/js/`:

### Credentials / auth
```
POST /aws/script-runner/accounts          aws-credentials.js:253
GET  /aws/authenticate                    (session check, implicit)
```

### Change management (per-tool, `{tool}` = script-runner | linux-qc-prep |
###                    linux-qc-post | sft-fixer | disk-recon | decom-survey)
```
GET  /aws/{tool}/list-changes             change-management.js:105
POST /aws/{tool}/load-change/{id}         change-management.js:74
POST /aws/{tool}/clear-change             change-management.js:302
POST /aws/{tool}/save-change-with-instances  change-management.js:238
POST /aws/{tool}/upload-change-csv        change-management.js:379
GET  /aws/script-runner/get-current-change   aws-change-management.js:346
POST /aws/script-runner/save-manual-change   aws-change-management.js:314
POST /aws/script-runner/upload-csv           aws-change-management.js:277
```

### Script runner
```
POST /aws/script-runner/validate-script   script-runner.js:194
POST /aws/script-runner/test-connectivity script-runner.js:101
POST /aws/script-runner/execute           script-runner.js:272  / aws-execution.js:39
GET  /aws/script-runner/results/{id}      script-runner.js:325
GET  /aws/script-runner/download-results/{id}  script-runner.js:510
GET  /aws/script-runner/library           script-runner.js:520
GET  /aws/script-runner/library/{id}      script-runner.js:562
GET  /aws/script-runner/last-execution-results  aws-execution.js:174
GET  /aws/script-runner/execution-status  aws-execution.js:105
```

### Linux QC Patching Prep
```
GET  /aws/linux-qc-prep/list-changes          linux-qc-patching-prep.js:206
POST /aws/linux-qc-prep/load-change/{id}      linux-qc-patching-prep.js:156
POST /aws/linux-qc-prep/clear-change          linux-qc-patching-prep.js:119
POST /aws/linux-qc-prep/test-connectivity     linux-qc-patching-prep.js:259
POST /aws/linux-qc-prep/execute-qc-step       linux-qc-patching-prep.js:393
GET  /aws/linux-qc-prep/qc-results/{id}       linux-qc-patching-prep.js:429
POST /aws/linux-qc-prep/execute-step2-multi-kernel  linux-qc-patching-prep.js (line ~)
GET  /aws/linux-qc-prep/latest-step1-results  linux-qc-patching-prep.js:32
POST /aws/linux-qc-prep/upload-change-csv     linux-qc-patching-prep.js (line ~)
GET  /aws/linux-qc-prep/download-reports      (window.location redirect)
GET  /aws/linux-qc-prep/download-final-report (window.location redirect)
```

### SFT Fixer
```
POST /aws/sft-fixer/validate-instance   sft-fixer.js:101
POST /aws/sft-fixer/execute-script      sft-fixer.js:224
GET  /aws/sft-fixer/batch-status/{id}   sft-fixer.js:276
```

### Other tools (disk-recon, vpc-recon, rhsa-compliance, decom-survey)
```
Pattern: POST /aws/{tool}/execute-*
         GET  /aws/{tool}/results/{id}  or  GET /aws/{tool}/status/{id}
         GET  /aws/{tool}/download-*
```
Full endpoint lists to be extracted per-tool during implementation.

---

## Gap summary

| Category | Status | Notes |
|---|---|---|
| Static HTML pages | Missing | Python serves Jinja2; need flattened HTML |
| CSRF | No work needed | JS sends no header if token absent; Go doesn't validate |
| Credentials POST | Path + format mismatch | JS sends FormData; Go expects JSON |
| Session current-change | Missing entirely | Core stateful pattern across all tools |
| Script runner execute | Path mismatch + shape adapter | Field names differ; needs session change lookup |
| Script runner results poll | Path mismatch | Alias to existing handler |
| Script validate / connectivity | Missing | New thin handlers |
| Script library | Path mismatch | Alias to `/api/scripts/` with template filter |
| Download results | Missing | New handler — stream CSV from DB |
| Change load/clear (per tool) | Missing | All map to same session endpoints |
| CSV upload | Missing | Parser + create/update Change + ChangeInstance |
| Tool execute (linux-qc, sft, etc.) | Missing | Thin wrappers via shared factory |
| Tool results poll (per tool) | Missing | Alias to `/api/exec/jobs/{id}` |

---

## Phase breakdown

### Phase 4.1 — Static asset migration
**Effort:** 0.5 day  
**Prerequisite:** none  
**Branch:** `feat/phase4-frontend-compat`

Tasks:
- [ ] Copy `~/git/CloudOpsTools/backend/static/` → `static/` in this repo
- [ ] Flatten each Jinja2 template to plain HTML:
  - Remove `{% extends %}`, `{% block %}`, `{% endblock %}`
  - Inline the base layout (`templates/base.html`) into each page
  - Remove `{{ current_change.* }}` injections (JS will fetch on load)
  - Replace `{{ url_for('static', path='...') }}` → `/static/...`
  - Remove `{{ csrf_token() }}` and any `<input type="hidden" name="csrfmiddlewaretoken">`
- [ ] Add routes in `server.go` to serve each HTML page:
  - `GET /` → `index.html`
  - `GET /aws/script-runner` → `aws/script_runner.html`
  - `GET /aws/linux-qc-prep` → `aws/linux_qc_patching_prep.html`
  - `GET /aws/linux-qc-post` → `aws/linux_qc_patching_post.html`
  - `GET /aws/sft-fixer` → `aws/sft_fixer.html`
  - `GET /aws/vpc-recon` → `aws/vpc_recon.html`
  - `GET /aws/disk-recon` → `aws/disk_recon.html`
  - `GET /aws/rhsa-compliance` → `aws/rhsa_compliance.html`
  - `GET /aws/decom-survey` → `aws/decom_survey.html`
- [ ] Add `WEB_DIR` config env var (path to flattened HTML files; default `./web`)
- [ ] Verify Go serves pages at correct URLs; check browser console for 404s

---

### Phase 4.2 — Credentials auth
**Effort:** 0.5 day  
**Prerequisite:** 4.1 (so pages load and you can test the auth form)

Tasks:
- [ ] Add route alias: `POST /aws/script-runner/accounts` → `handleCreateCredentials`
- [ ] Update `handleCreateCredentials` to accept **both** `multipart/form-data`
  and `application/json`:
  - Check `Content-Type` header
  - FormData field mapping: `access_key_id`, `secret_access_key`,
    `session_token` (optional), `environment` (`com`/`gov`)
- [ ] Add route alias: `GET /aws/authenticate` → `handleSessionStatus`
- [ ] Smoke test: load auth page, submit credentials, verify session cookie set

---

### Phase 4.3 — Session current-change state
**Effort:** 1 day  
**Prerequisite:** 4.2 (credentials must work before loading changes)

This is the core stateful primitive that all tool pages depend on.

New file: `internal/api/session_change.go`

New endpoints:
```
POST   /api/changes/session/load/{id}   — write change_id to session cookie
GET    /api/changes/session/current     — read change_id from cookie, fetch Change+Instances from DB
DELETE /api/changes/session/current     — clear change_id from cookie
```

Session key: `"current_change_id"` (string, stored in existing encrypted cookie via
`middleware.SessionConfig`).

Route aliases for all tool namespaces (single handler each):
```
POST /aws/{tool}/load-change/{id}    → handleSessionLoadChange
POST /aws/{tool}/clear-change        → handleSessionClearChange
GET  /aws/{tool}/list-changes        → handleListChanges  (already exists)
GET  /aws/script-runner/get-current-change → handleSessionGetCurrentChange
```

`{tool}` wildcard covers: `script-runner`, `linux-qc-prep`, `linux-qc-post`,
`sft-fixer`, `disk-recon`, `rhsa-compliance`, `decom-survey`.

Note on `save-change-with-instances`: the Python handler merges a manually-selected
instance list into the session change. In Go, since we're stateless (only storing
change_id), this reduces to `load-change/{id}` — the instance list is always fetched
fresh from DB via `GET /api/changes/session/current`. If the JS sends a POST to
`save-change-with-instances`, alias it to `load-change/{id}` using the change_id
from the request body.

Tasks:
- [ ] Implement `handleSessionLoadChange`, `handleSessionGetCurrentChange`,
  `handleSessionClearChange` in `session_change.go`
- [ ] Register all tool-namespace aliases in `server.go`
- [ ] Write unit tests for session load/get/clear handlers

---

### Phase 4.4 — Script runner execution
**Effort:** 1.5 days  
**Prerequisite:** 4.3 (session change needed to resolve instance list)

New file: `internal/api/compat_exec.go`

Tasks:

**Execute adapter** (`POST /aws/script-runner/execute`):
- [ ] Read current change from session → get `instance_ids`, `account_id`, `region`, `platform`
- [ ] Map JS payload `{script_id, change_id?, inline_script?}` to
  `runner.Start()` parameters
- [ ] Return `{batch_id, status}` matching what `script-runner.js` polls for

**Results poll** (`GET /aws/script-runner/results/{batch_id}`):
- [ ] Alias to `handleGetJob`; verify response shape matches what
  `script-runner.js:325` expects: `{status, results: [{instance_id, status, output, error}]}`
- [ ] Add shape adapter if needed

**Validate script** (`POST /aws/script-runner/validate-script`):
- [ ] Port pattern-check logic from Python (dangerous command detection)
- [ ] Returns `{valid: bool, warnings: []string}`

**Test connectivity** (`POST /aws/script-runner/test-connectivity`):
- [ ] Alias to `POST /api/exec/script` with an inline `echo ok` ping script
- [ ] Uses current session change instances

**Script library** (`GET /aws/script-runner/library`, `GET /aws/script-runner/library/{id}`):
- [ ] Alias to existing `/api/scripts/` handlers with `is_template=true` filter

**Last execution results** (`GET /aws/script-runner/last-execution-results`):
- [ ] Query DB for most recent `ExecutionBatch` for the current session change
- [ ] Return same shape as results poll

**Execution status** (`GET /aws/script-runner/execution-status`):
- [ ] Alias to `GET /api/exec/jobs/{id}` — but this endpoint takes a query param
  `?batch_id=` rather than a path segment; check JS call site and adapt

**Download results** (`GET /aws/script-runner/download-results/{id}`):
- [ ] Stream `Execution` rows for batch as CSV
- [ ] Columns: `instance_id`, `status`, `exit_code`, `output`, `error`, `completed_at`
- [ ] Content-Type: `text/csv`, Content-Disposition: `attachment`

---

### Phase 4.5 — Tool-specific execution handlers
**Effort:** 1.5 days  
**Prerequisite:** 4.4 (shared execution infrastructure must exist)

All tool-specific endpoints (`linux-qc-prep`, `linux-qc-post`, `sft-fixer`,
`disk-recon`, `rhsa-compliance`, `decom-survey`) reduce to the same pattern:

```
POST /aws/{tool}/execute-*   → look up session change → POST /api/exec/script with tool script
GET  /aws/{tool}/results/{id} → GET /api/exec/jobs/{id}
GET  /aws/{tool}/status/{id}  → GET /api/exec/jobs/{id}  (different tools use different names)
```

New file: `internal/api/compat_tools.go`

Implement a **shared tool execution factory**:
```go
func (s *Server) toolExecHandler(scriptContent string, platform string) http.HandlerFunc
```

This reads the current session change, validates it matches `platform`, resolves
instance list, and calls `runner.Start()` with the provided script. Each tool's
execute endpoint is a one-liner:

```go
s.mux.HandleFunc("POST /aws/linux-qc-prep/execute-qc-step",
    s.toolExecHandler(scripts.LinuxQCStep1, "linux"))
```

Script content is stored as embedded Go string constants (one `.go` file per tool)
or loaded from `static/scripts/` at startup. Use embedded strings for now — they
can be moved to DB-backed Script records later.

Tasks per tool:
- [ ] `linux-qc-prep`: `execute-qc-step`, `execute-step2-multi-kernel`,
  `qc-results/{id}`, `latest-step1-results`, `download-reports`, `download-final-report`
- [ ] `linux-qc-post`: `execute-post-validation`, `validation-results/{id}`
- [ ] `sft-fixer`: `validate-instance`, `execute-script`, `batch-status/{id}`
- [ ] `disk-recon`: `execute`, `results/{id}`, `download`
- [ ] `rhsa-compliance`: `execute`, `results/{id}`
- [ ] `decom-survey`: `execute`, `results/{id}`, `download`

Download endpoints for each tool: same CSV streaming handler as Phase 4.4.

---

### Phase 4.6 — CSV upload for changes
**Effort:** 0.5 day  
**Prerequisite:** 4.3

New handler: `handleUploadChangesCSV` in `internal/api/changes.go`

Routes:
```
POST /aws/script-runner/upload-csv
POST /aws/script-runner/upload-change-csv
POST /aws/{tool}/upload-change-csv   (all tool namespaces)
```

Expected CSV columns (from Python source): `instance_id`, `account_id`, `region`,
`platform`. Optional: `name` (stored in `instance_metadata`).

Tasks:
- [ ] Parse `multipart/form-data` file upload
- [ ] Validate each row (reuse validation patterns from `utils.js` server-side)
- [ ] Accept either a `change_number` form field or generate one
- [ ] Create or update `Change` + `ChangeInstance` records in a transaction
- [ ] Return created change object; JS loads it into session via `load-change/{id}`

---

### Phase 4.7 — Save manual change
**Effort:** 0.25 day  
**Prerequisite:** 4.3

Route: `POST /aws/script-runner/save-manual-change`

JS sends: `{change_number, instance_ids: []}` (raw string list from textarea).

Handler:
- Create `Change` with provided `change_number` (or conflict → return existing)
- For each instance_id, create `ChangeInstance` with `account_id` and `region` from
  the current session credentials (or accept from form if JS sends them)
- Load the new change into session
- Return new change object

---

### Phase 4.8 — Database and smoke testing
**Effort:** 0.25 day  
**Prerequisite:** all phases complete

Tasks:
- [ ] `cp ~/git/CloudOpsTools/data/cloudopstools.db ~/git/GOrg-CloudTools/data/cloudopstools.db`
- [ ] Run Go server: `go run ./cmd/server`
- [ ] AutoMigrate adds nullable Go-specific columns without disturbing existing rows
- [ ] Smoke test checklist:
  - [ ] Auth page loads; can submit credentials; session cookie set
  - [ ] Script Runner page loads; change list populates; can load a change
  - [ ] Can execute an inline script against a loaded change; results poll works
  - [ ] Script library populates; can select and execute a saved script
  - [ ] CSV upload creates a change
  - [ ] Linux QC Prep page: can load change, test connectivity, execute step 1
  - [ ] SFT Fixer: validate instance, execute, poll results

---

## Out of scope for Phase 4

These are explicitly deferred — stub endpoints that return `501 Not Implemented`:

| Feature | Reason |
|---|---|
| Linux QC step 2 multi-kernel | Complex multi-batch orchestration; separate ticket |
| Download report (PDF/ZIP) | Python has complex report rendering; return raw CSV for now |
| GCP auth page | No GCP backend yet (Phase 3) |
| VPC recon full workflow | Needs vpc package wired to frontend; separate ticket |
| `POST /aws/script-runner/accounts` FormData legacy fields beyond basic creds | Low priority |

---

## Config additions needed

| Env var | Default | Purpose |
|---|---|---|
| `WEB_DIR` | `./web` | Path to flattened HTML pages |
| `STATIC_DIR` | `./static` | Already exists; path to CSS/JS/images |

---

## Key decisions to revisit

1. **Script content storage**: Phase 4.5 proposes embedded Go string constants for
   tool scripts. If the scripts need frequent updates, move to DB-backed `Script`
   records (already have the model) so they can be edited via the script library UI.

2. **`save-change-with-instances` semantics**: Currently proposed to alias to
   `load-change/{id}`. If the JS sends a manually-curated subset of instances (not
   the full change instance list), this breaks. Verify with the JS before implementing.

3. **Multi-account execution**: The current session model stores one `current_change_id`.
   If a change spans multiple accounts or regions, the execute handler needs to fan out
   across accounts. The existing `OrgRunner` does this but requires org-level credentials.
   Decide whether single-account execution (Phase 4.4) is sufficient for Phase 4 scope.

4. **ServiceNow seam for changes**: Phase 4 keeps changes as manual DB records.
   When ServiceNow is approved, a `ChangeProvider` interface wraps the DB layer
   (see earlier design discussion). No code change needed in the API or frontend.

---

## Related specs

- `docs/specs/phase1-architecture.md` — package restructure, RemoteExecutor interface
- `docs/specs/phase2-tui.md` — TUI frontend (deferred; stubs remain in main)
- `docs/specs/phase3-multicloud.md` — Azure/GCP (deferred)
