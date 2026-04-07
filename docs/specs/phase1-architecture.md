# Phase 1 — Architecture Prep

**Goal:** Restructure the codebase to cleanly separate OS-level tooling from
cloud-provider tooling, and establish the package layout and interfaces that
Phase 2 (TUI) and Phase 3 (multi-cloud) will build on. No new user-facing
features. All existing tests must pass at the end of this phase.

---

## 1. Package restructure

Move all AWS-specific packages under a `cloud/` namespace to make room for
future providers without touching the `internal/` root.

```text
Before                          After
──────────────────────────────────────────────────────
internal/aws/credentials/   →   internal/cloud/aws/credentials/
internal/aws/ec2/           →   internal/cloud/aws/ec2/
internal/aws/ssm/           →   internal/cloud/aws/ssm/
internal/aws/vpc/           →   internal/cloud/aws/vpc/
```

Stub directories for future providers (empty packages with a single doc file,
no implementation):

```text
internal/cloud/azure/   (stub)
internal/cloud/gcp/     (stub)
```

All import paths in `internal/exec/`, `internal/api/`, `cmd/server/`, and
tests update to the new paths. No logic changes — pure rename.

---

## 2. Interface rename: SSMExecutor → RemoteExecutor

The `SSMExecutor` interface in `internal/exec/runner.go` is already the right
abstraction. The name leaks the AWS implementation detail. Rename it so all
three cloud providers can implement it without confusion.

```go
// internal/exec/executor.go  (extract from runner.go into its own file)

// RemoteExecutor is the cloud-agnostic interface for running a script on a
// remote instance and waiting for the result. AWS SSM, Azure RunCommand, and
// GCP RunCommand all implement this interface.
type RemoteExecutor interface {
    Send(ctx context.Context, instanceIDs []string, script, platform string) (commandID string, err error)
    WaitForDone(ctx context.Context, commandID, instanceID string) (*InvocationResult, error)
}
```

### InvocationResult (renamed from ssm.InvocationStatus)

`ssm.InvocationStatus` leaked into the `exec` package via the mock interface.
It was moved to `internal/exec/` as `InvocationResult` — the canonical type
across all providers. Provider adapters (e.g. `ssmAdapter` in `runner.go`)
translate the cloud-specific status type to `InvocationResult` before returning.

```go
// internal/exec/executor.go

type InvocationResult struct {
    CommandID  string
    InstanceID string
    Status     string  // "Success", "Failed", "TimedOut", "Cancelled"
    Output     string
    Error      string
    ExitCode   int
    Done       bool
}
```

`ssm.Executor.WaitForDone()` returns `*ssm.InvocationStatus` internally.
The `ssmAdapter.WaitForDone()` wraps it and returns `*InvocationResult`.
`ssm.InvocationStatus` stays internal to the `ssm` package and does not
appear in any signature outside it.

---

## 3. Tool model — add Scope field

The `Tool` model needs an explicit distinction between tools that are
OS-level (run on any cloud's instances via a remote executor) and tools that
call cloud-provider APIs directly.

```go
// internal/db/models/script.go

type Tool struct {
    gorm.Model
    Name        string   `gorm:"uniqueIndex;not null" json:"name"`
    Description string   `gorm:"type:text" json:"description"`
    ToolType    string   `gorm:"not null" json:"tool_type"`
    // Scope distinguishes tools that run ON instances (cloud-agnostic)
    // from tools that call cloud-provider APIs directly.
    //
    //   "os"    — script runs on any cloud's Linux or Windows instance via
    //             the provider's remote execution agent (SSM, RunCommand, etc.)
    //             Appears in the TUI regardless of which cloud env is active.
    //
    //   "cloud" — calls cloud-provider APIs directly (VPC recon, org traversal).
    //             Only appears when the matching provider's credentials are loaded.
    Scope       string   `gorm:"not null;default:os" json:"scope"`
    Platform    string   `gorm:"" json:"platform"`
    ScriptPath  string   `gorm:"" json:"script_path"`
    Scripts     []Script `gorm:"foreignKey:ToolID" json:"scripts,omitempty"`
}
```

Migration: `Scope` defaults to `"os"` so existing tool records are valid
without a data migration. Cloud-specific tools (VPC recon etc.) are updated
to `scope = "cloud"` in a seed or migration step.

Valid values: `"os"` | `"cloud"`. Additional provider-scoped values
(`"aws"`, `"azure"`, `"gcp"`) are reserved for Phase 3 if finer-grained
filtering is needed.

---

## 4. Credential model — decouple from HTTP session

`internal/cloud/aws/credentials` currently has two concerns:

- `FromSession()` — extracts credentials from an encrypted HTTP session cookie.
  This is HTTP/browser-specific and will be removed when the TUI is the
  primary interface.
- `Validate()` — calls STS GetCallerIdentity to confirm credentials work.
  This is cloud logic and stays.
- `HomeRegion()` — pure mapping of env string to region. Stays.

Action: mark `FromSession()` as deprecated in a doc comment. It remains
functional so `cmd/server` continues to compile, but Phase 2 introduces a
TUI-native credential path that does not use it.

---

## 5. cmd/server — keep as optional HTTP daemon

The HTTP API server (`cmd/server`) is retained. Rationale:

- It provides a scriptable interface to the DB and execution engine from the
  bastion without opening the TUI (useful for automation and admin queries).
- Keeping it costs nothing — it shares all packages with the TUI binary.
- It can be disabled in deployment by simply not running it.

No functional changes to `cmd/server` in Phase 1. It continues to use
`FromSession()` and the cookie auth model unchanged.

`cmd/tui/` is added in Phase 2 as a parallel entry point.

---

## 6. Acceptance criteria

- [ ] All packages compile under the new `internal/cloud/aws/` paths
- [ ] All existing tests pass without modification to test logic
  (only import path updates)
- [ ] `RemoteExecutor` interface and `ExecutionResult` type are in
  `internal/exec/executor.go`
- [ ] `SSMExecutor` name is gone from all files outside the ssm package
- [ ] `Tool.Scope` field exists in the model and `go test ./...` passes
- [ ] `internal/cloud/azure/` and `internal/cloud/gcp/` stub dirs exist
- [ ] `deferred-testing.md` updated to reflect new package paths
- [ ] `CLAUDE.md` package layout section updated

---

## 7. What does NOT change in Phase 1

- No TUI code
- No changes to handler logic in `internal/api/`
- No changes to `internal/exec/runner.go` logic (only the interface name
  and result type)
- No changes to DB schema beyond adding `Tool.Scope`
- No new dependencies
