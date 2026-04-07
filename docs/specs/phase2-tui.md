# Phase 2 — TUI Frontend

**Goal:** Replace the web frontend with a terminal user interface that runs
directly on bastion servers. The TUI is the primary user interface for all
operational workflows. Authentication is handled entirely by the bastion access
stack (VPN → SFT → SSH → system user); the TUI adds per-cloud-environment
credential management on top.

---

## 1. Entry point

```text
cmd/tui/main.go
```

Alongside the existing `cmd/server/main.go`. Both binaries share all
`internal/` packages. Deployment on the bastion installs both; `cloudtools`
is the TUI binary, `cloudtools-server` is the optional HTTP daemon.

### Startup sequence

```text
1. Open / migrate SQLite DB (same path as server: $DATABASE_URL or default)
2. Load config from environment (reuses internal/config)
3. Initialise bubbletea program
4. Launch main menu
```

There is no listening port or HTTP and no credential check at startup; the
app is fully usable (browse scripts, tools, history) without any cloud
credentials loaded.

---

## 2. Dependencies

| Package | Version | Purpose |
|---|---|---|
| `github.com/charmbracelet/bubbletea` | `v2.0.2` | TUI framework (Elm architecture) |
| `github.com/charmbracelet/lipgloss` | `v2.0.2` | Layout and styling |
| `github.com/charmbracelet/bubbles` | `v0.21.1` | table, textinput, spinner, viewport sub-packages |

All Charm packages. Single maintainer, stable API, well-documented.

To add when Phase 2 development begins:

```bash
go get github.com/charmbracelet/bubbletea@v2.0.2 \
       github.com/charmbracelet/lipgloss@v2.0.2 \
       github.com/charmbracelet/bubbles@v0.21.1
go mod tidy
```

---

## 3. Screen inventory

### 3.1 Main Menu

Entry point after startup.

```text
╔═ GOrg CloudTools ══════════════════════════════════╗
║                                                     ║
║   [O] OS Tools                                      ║
║   [C] Cloud Tools                                   ║
║   [S] Script Library                                ║
║   [T] Tool Library                                  ║
║   [H] Job History                                   ║
║   [X] Changes                                       ║
║                                                     ║
║   AWS COM: ● active    AWS GOV: ○ no credentials    ║
║                                                     ║
║   [A] Add/Refresh Credentials    [Q] Quit           ║
╚═════════════════════════════════════════════════════╝
```

Cloud environment status indicators appear at the bottom of every screen:
`●` = credentials loaded and validated, `○` = no credentials.

---

### 3.2 Credential Input (modal overlay)

Triggered by `[A]` from any screen, or automatically when the user attempts
an action that requires cloud credentials that are not yet loaded.

```text
╔═ AWS Credentials ══════════════════════════════════╗
║                                                     ║
║  Environment  [ COM ▼ ]                             ║
║                                                     ║
║  Access Key ID                                      ║
║  > ASIA________________________                     ║
║                                                     ║
║  Secret Access Key                                  ║
║  > ************************************             ║
║                                                     ║
║  Session Token (optional)                           ║
║  > ________________________________________         ║
║                                                     ║
║  [ Validate & Activate ]    [ Cancel ]              ║
╚═════════════════════════════════════════════════════╝
```

On submit:
1. `isValidAWSKeyID()` check (reuse from `internal/api/auth.go` → move to
   `internal/cloud/aws/credentials/`)
2. `containsXSS()` check on secret and token
3. Build `aws.Config` via `awsconfig.LoadDefaultConfig` with static provider
4. Call `credentials.Validate()` (STS GetCallerIdentity)
5. On success: store `aws.Config` in TUI model's `cloudEnvs` map, update
   status indicator

Credentials live in the TUI model for the session duration only. They are
never written to disk. On process exit they are gone.

---

### 3.3 OS Tools Menu

Lists all `Tool` records with `scope = "os"`, grouped by platform.

```text
╔═ OS Tools ══════════════════════════════════════════╗
║                                                     ║
║  Linux                                              ║
║  ──────────────────────────────────────             ║
║  › Patching QC          Pre-patch system check      ║
║    Disk Recon            Disk usage and health      ║
║    Decom Survey          Pre-decommission audit     ║
║                                                     ║
║  Windows                                            ║
║  ──────────────────────────────────────             ║
║    Patching QC          Pre-patch system check      ║
║                                                     ║
║  [↑↓] Navigate   [Enter] Select   [Esc] Back        ║
╚═════════════════════════════════════════════════════╝
```

Selecting a tool proceeds to Instance Selector (3.5).

---

### 3.4 Cloud Tools Menu

Lists all `Tool` records with `scope = "cloud"`, grouped by provider.
Tools for providers without loaded credentials are shown dimmed with a
`[no credentials]` label. Selecting a dimmed tool triggers the Credential
Input modal.

```text
╔═ Cloud Tools ═══════════════════════════════════════╗
║                                                     ║
║  AWS COM  ●                                         ║
║  ──────────────────────────────────────             ║
║  › VPC Recon             Describe VPCs/subnets/SGs  ║
║    Org Accounts          List accounts in org       ║
║                                                     ║
║  AWS GOV  ○                                         ║
║  ──────────────────────────────────────             ║
║    VPC Recon             [no credentials]           ║
║    Org Accounts          [no credentials]           ║
║                                                     ║
║  Azure    ○  (coming soon)                          ║
║  GCP      ○  (coming soon)                          ║
║                                                     ║
║  [↑↓] Navigate   [Enter] Select   [Esc] Back        ║
╚═════════════════════════════════════════════════════╝
```

---

### 3.5 Instance Selector

Used by OS tools (and any cloud tool that targets instances). Calls
`ec2.ListRunning()` using the active cloud env's credentials to populate
the table. For multi-cloud in Phase 3, the selector can aggregate instances
across providers.

```text
╔═ Select Instances ══════════════════════════════════╗
║  Filter: [________________]  Platform: [Linux ▼]    ║
║                                                     ║
║  ┌──────────────┬───────────┬────────┬────────────┐ ║
║  │ Instance ID  │ Name      │ Acct   │ Region     │ ║
║  ├──────────────┼───────────┼────────┼────────────┤ ║
║  │ ☑ i-0abc123  │ web-01    │ 123456 │ us-east-1  │ ║
║  │ ☑ i-0def456  │ web-02    │ 123456 │ us-east-1  │ ║
║  │ ☐ i-0ghi789  │ db-01     │ 123456 │ us-west-2  │ ║
║  └──────────────┴───────────┴────────┴────────────┘ ║
║                                                     ║
║  3 instances  2 selected                            ║
║  [Space] Toggle  [A] All  [Enter] Run  [Esc] Back   ║
╚═════════════════════════════════════════════════════╝
```

---

### 3.6 Execution View

Shown immediately after job submission. Polls `db.ExecutionBatch` and its
child `Execution` records on a ticker. Does NOT call SSM directly — the
background goroutine (started by `exec.Runner.Start()`) writes to the DB;
the TUI reads from it.

```text
╔═ Job #42 — Patching QC ════════════════════════════╗
║  Status: Running          Started: 14:32:07         ║
║  ████████░░░░░░░░░░░░  3 / 10                       ║
║                                                     ║
║  ┌──────────────┬─────────────┬──────┬───────────┐  ║
║  │ Instance     │ Status      │ Exit │ Duration  │  ║
║  ├──────────────┼─────────────┼──────┼───────────┤  ║
║  │ i-0abc123    │ ✓ Completed │  0   │  12s      │  ║
║  │ i-0def456    │ ✓ Completed │  0   │  14s      │  ║
║  │ i-0ghi789    │ ✗ Failed    │  1   │  10s      │  ║
║  │ i-0jkl012    │ ⟳ Running  │  —   │  18s      │  ║
║  │ i-0mno345    │ ⟳ Running  │  —   │  15s      │  ║
║  │ i-0pqr678    │ ○ Pending   │  —   │  —        │  ║
║  └──────────────┴─────────────┴──────┴───────────┘  ║
║                                                     ║
║  [Enter] View Output   [Esc] Back (job continues)   ║
╚═════════════════════════════════════════════════════╝
```

Navigating away does not cancel the job — it continues in background.
The job is accessible from Job History (3.8) at any time.

---

### 3.7 Output Viewer

Scrollable viewport showing stdout/stderr for a single instance execution.

```text
╔═ Output: i-0abc123 — Patching QC ══════════════════╗
║  Exit code: 0   Duration: 12s                       ║
║  ─────────────────────────────────────────────────  ║
║  [stdout]                                           ║
║  Checking kernel version... OK (5.15.0-91-generic)  ║
║  Checking pending reboots... NONE                   ║
║  Checking disk space /... OK (42% used)             ║
║  Checking disk space /var... OK (31% used)          ║
║  All checks passed.                                 ║
║                                                     ║
║  [stderr]                                           ║
║  (empty)                                            ║
║                                                     ║
║  [↑↓/PgUp/PgDn] Scroll   [Esc] Back                ║
╚═════════════════════════════════════════════════════╝
```

---

### 3.8 Job History

Lists recent `ExecutionBatch` records. Shows interrupted jobs with a
`[resume]` action that triggers the Credential Input modal if creds are
not loaded, then calls `exec.Runner.Resume()`.

```text
╔═ Job History ═══════════════════════════════════════╗
║                                                     ║
║  ┌───┬────────────────┬──────────────┬───────────┐  ║
║  │ # │ Tool / Script  │ Status       │ Started   │  ║
║  ├───┼────────────────┼──────────────┼───────────┤  ║
║  │42 │ Patching QC    │ ⟳ Running   │ 14:32     │  ║
║  │41 │ Disk Recon     │ ✓ Completed  │ 13:15     │  ║
║  │40 │ Inline Script  │ ✗ Failed     │ 12:48     │  ║
║  │39 │ VPC Recon      │ ! Interrupted│ Yesterday │  ║
║  └───┴────────────────┴──────────────┴───────────┘  ║
║                                                     ║
║  [Enter] View Detail   [R] Resume (interrupted)     ║
║  [Esc] Back                                         ║
╚═════════════════════════════════════════════════════╝
```

---

### 3.9 Script Library

CRUD interface for the script catalog. Inline script editor using
`textinput` / `textarea` bubbles. Ephemeral scripts are not shown.

---

### 3.10 Change Management

Create/view/update change records. Associates instances and scripts with
a change number for audit purposes.

---

## 4. TUI model structure

```go
// cmd/tui/model.go

type CloudEnv struct {
    Cfg       aws.Config
    AccountID string
    UserARN   string
}

type Model struct {
    // Active screen
    screen     Screen

    // DB connection (shared with all operations)
    db         *gorm.DB

    // Config
    cfg        *config.Config

    // Cloud credentials, keyed by env ("aws-com", "aws-gov")
    // nil entry = not authenticated for that env
    cloudEnvs  map[string]*CloudEnv

    // Screen-local state (active child model)
    active     tea.Model

    // Terminal dimensions
    width, height int

    // Error to display (cleared on next keypress)
    err        error
}
```

---

## 5. Async execution in bubbletea

bubbletea handles async work via `tea.Cmd` — a function that runs in a
goroutine and returns a `tea.Msg`. The DB-polling pattern fits naturally:

```go
// Poll the DB for batch status every 2 seconds
func pollBatch(db *gorm.DB, batchID uint) tea.Cmd {
    return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
        var batch models.ExecutionBatch
        db.Preload("Executions").First(&batch, batchID)
        return batchUpdatedMsg{batch: batch}
    })
}
```

The goroutine that actually runs the SSM commands (`exec.Runner.Start()`)
writes to the DB as before. The TUI only reads from the DB — it never
calls SSM directly. This keeps the TUI layer thin and testable.

---

## 6. VPC Recon and cloud-specific tool output

Cloud tools that return structured data (VPC recon, org accounts) render
in a multi-pane view rather than the raw output viewer:

```text
╔═ VPC Recon — us-east-1 ════════════════════════════╗
║  VPC: vpc-0abc123 (my-prod-vpc)                     ║
║  CIDR: 10.0.0.0/16                                  ║
║                                                     ║
║  Subnets (4)              Security Groups (6)       ║
║  ┌───────────────────┐   ┌───────────────────────┐  ║
║  │ subnet-0a  10.0.1 │   │ sg-0abc  web-tier     │  ║
║  │ subnet-0b  10.0.2 │   │ sg-0def  app-tier     │  ║
║  │ subnet-0c  10.0.3 │   │ sg-0ghi  db-tier      │  ║
║  └───────────────────┘   └───────────────────────┘  ║
║                                                     ║
║  [E] Export JSON   [Esc] Back                       ║
╚═════════════════════════════════════════════════════╝
```

---

## 7. Acceptance criteria

- [ ] `cmd/tui` compiles and launches on Linux (bastion target OS)
- [ ] Main menu renders with correct cloud env status indicators
- [ ] Credential input validates format, calls STS, stores in model
- [ ] OS tools screen lists tools with `scope = "os"` correctly grouped
- [ ] Cloud tools screen shows correct enabled/disabled state per env
- [ ] Instance selector populates from `ec2.ListRunning()` when creds present
- [ ] Execution view polls DB and updates in real time
- [ ] Output viewer renders scrollable stdout/stderr
- [ ] Job history shows interrupted jobs with resume action
- [ ] Navigating away from execution view does not cancel the job
- [ ] `cmd/server` still compiles and passes all existing tests
- [ ] No new `internal/api` dependencies introduced in `cmd/tui`
