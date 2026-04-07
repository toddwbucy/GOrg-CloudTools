# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## GitHub Access

All GitHub operations in this workspace must use the `github-work` SSH host alias, **not** `github.com`. The remote is already configured correctly:

```
git@github-work:toddwbucy/GOrg-CloudTools.git
```

## Build & Test

```bash
go build ./...
go test ./...
go test ./internal/... -run TestName   # single test
go vet ./...
```

The server entry point is `cmd/server/main.go`. All configuration comes from environment variables (see `internal/config/config.go`).

## Project Purpose

Go rewrite of `~/git/CloudOpsTools` (Python/FastAPI). Primary motivation: eliminate Python's `.pth` file supply-chain attack surface. The Go binary has no equivalent — the dependency tree is baked in at compile time.

## Architecture

### Core principle

The backend exposes **composable execution primitives**. The frontend assembles them into workflows. The backend has no concept of "Linux QC" or "Script Runner" as named workflows — those are frontend concerns.

Every workflow that runs on EC2 instances (RHSA check, disk recon, Linux QC, decom survey, script runner) reduces to the same primitive:

```
POST /api/exec/script   →   SSM SendCommand   →   {job_id}
```

VPC recon is the only workflow using a different primitive (EC2 API, not SSM).

### Package layout

```
cmd/server/           Entry point — wires config, DB, OrgRunner, HTTP server

internal/
  config/             Env-var configuration
  db/
    db.go             GORM + pure-Go SQLite (WAL mode); AutoMigrate on startup
    models/           ORM models:
                        account.go   — Account, Region, Instance
                        script.go    — Script, Tool
                        execution.go — Execution, ExecutionBatch
                        session.go   — ExecutionSession (groups batches for audit)
                        change.go    — Change, ChangeInstance

  exec/
    executor.go       RemoteExecutor interface (cloud-agnostic; AWS/Azure/GCP all implement this)
    runner.go         Batch script execution: resolves script, fans out SSM, writes DB
    org.go            Org-wide execution via gorg-aws visitor + runner

  cloud/
    aws/
      credentials/    FromSession() → aws.Config (deprecated for TUI); Validate() via STS
      ssm/            SSM SendCommand + polling (the universal execution primitive)
      ec2/            EC2 DescribeInstances paginator
      vpc/            DescribeVpcs/Subnets/SecurityGroups (VPC workflow primitive)
    azure/            (stub — Phase 3)
    gcp/              (stub — Phase 3)

  api/
    server.go         HTTP server, route registration (Go 1.22 ServeMux)
    exec.go           POST /api/exec/script|org-script, GET /api/exec/jobs/{id}
    awsresources.go   GET /api/aws/instances|vpcs|org/accounts
    sessions.go       POST|GET|PATCH /api/sessions
    auth.go           POST/GET/DELETE /api/auth/aws-credentials
    scripts.go        CRUD /api/scripts/
    tools.go          GET /api/tools/
    health.go         GET /health, GET /api/health
    helpers.go        jsonOK / jsonError
    middleware/
      session.go      AES-256-GCM encrypted cookie (no Redis)
      ratelimit.go    Per-IP token bucket (golang.org/x/time/rate)
      cors.go         Origin allow-list CORS
```

### API surface

```
# Script execution (covers ALL SSM-based workflows)
POST /api/exec/script              {script_id|inline_script, instance_ids, platform, session_id?}
POST /api/exec/org-script          {script_id|inline_script, env, parent_id?, platform, session_id?}
GET  /api/exec/jobs/{id}           → batch status + per-instance results

# AWS resource queries
GET  /api/aws/instances            ?account_id=&region=&platform=
GET  /api/aws/vpcs                 ?account_id=&region=&vpc_id=
GET  /api/aws/org/accounts         ?env=&parent_id=

# Session management (audit trail, multi-step workflow grouping)
POST /api/sessions
GET  /api/sessions/
GET  /api/sessions/{id}            → session + all batches + all executions
PATCH /api/sessions/{id}/status

# Auth
POST   /api/auth/aws-credentials   validates via STS, stores in encrypted cookie
GET    /api/auth/aws-credentials/{environment}
DELETE /api/auth/aws-credentials/{environment}
GET    /api/auth/session-status

# Script/tool library
GET|POST        /api/scripts/
GET|PUT|DELETE  /api/scripts/{id}
GET             /api/tools/
GET             /api/tools/{id}

# Health
GET /health
GET /api/health
```

### Key design decisions

**Sessions** — AES-256-GCM encrypted HttpOnly cookies. No Redis. Key derived via `sha256(SECRET_KEY)`. GCM tag provides tamper detection.

**Rate limiting** — `golang.org/x/time/rate` per-IP token bucket, per-route group. No external dependency.

**Database** — GORM + `github.com/glebarez/sqlite` (pure Go, no CGo). WAL mode. JSON fields use GORM's built-in `serializer:json` tag.

**Org traversal** — `github.com/toddwbucy/gorg-aws`. OrgRunner is initialised at startup if server-side management credentials are present; org endpoints return 503 otherwise. Per-account workflows use session credentials via `credentials.FromSession()`.

**Async execution** — `runner.Start()` and `orgRunner.Start()` return a `job_id` immediately and run in a detached goroutine. Frontend polls `GET /api/exec/jobs/{id}`.

**Coupling** — The frontend and backend are domain-coupled (both understand scripts, instances, jobs) but technically decoupled (backend does not know about "Linux QC" or any named workflow; the API contract is the boundary).

### Dependency policy

| Package | Purpose |
|---|---|
| `github.com/toddwbucy/gorg-aws` | AWS org traversal |
| `github.com/aws/aws-sdk-go-v2/*` | AWS SDK (transitive via gorg-aws) |
| `golang.org/x/time/rate` | Rate limiting |
| `gorm.io/gorm` | ORM |
| `github.com/glebarez/sqlite` | Pure-Go SQLite driver |

Do not add new dependencies without considering supply-chain impact. Prefer stdlib.

## Environment Variables

| Variable | Default | Notes |
|---|---|---|
| `SECRET_KEY` | random (dev) | ≥32 chars required in production |
| `ENVIRONMENT` | `development` | Set `production` to enforce security constraints |
| `DATABASE_URL` | `./data/cloudopstools.db` | SQLite file path |
| `PORT` | `8500` | |
| `AWS_ACCESS_KEY_ID_COM` | — | Server-side mgmt credentials (enables OrgRunner) |
| `AWS_SECRET_ACCESS_KEY_COM` | — | |
| `AWS_SESSION_TOKEN_COM` | — | |
| `AWS_ACCESS_KEY_ID_GOV` | — | |
| `AWS_SECRET_ACCESS_KEY_GOV` | — | |
| `AWS_SESSION_TOKEN_GOV` | — | |
| `DEV_MODE` | `false` | Skips STS credential validation |
| `MAX_CONCURRENT_EXECUTIONS` | `5` | Per-batch SSM concurrency |
| `EXECUTION_TIMEOUT` | `1800` | SSM command timeout (seconds) |
| `RATE_LIMIT_AUTH_ENDPOINTS` | `10/minute` | |
| `RATE_LIMIT_EXECUTION_ENDPOINTS` | `5/minute` | |
| `RATE_LIMIT_READ_ENDPOINTS` | `100/minute` | |
