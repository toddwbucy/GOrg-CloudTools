# Phase 3 — Multi-Cloud Expansion

**Goal:** Extend the execution and discovery primitives to Azure and GCP so
that OS-level tools (scripts) run identically against instances on all three
providers, and each provider's cloud-specific tools (network recon, org/tenant
traversal) are available through the same TUI interface established in Phase 2.

This phase is intentionally deferred until Phase 1 and Phase 2 are stable and
deployed. The package structure laid down in Phase 1 means adding a provider
is additive — no existing code changes.

---

## 1. The core contract

All three providers must satisfy `exec.RemoteExecutor`:

```go
type RemoteExecutor interface {
    Send(ctx context.Context, instanceIDs []string, script, platform string) (commandID string, err error)
    WaitForDone(ctx context.Context, commandID, instanceID string) (*ExecutionResult, error)
}
```

`exec.Runner` is already written against this interface. The same batch
execution, concurrency control, DB persistence, and startup recovery logic
applies to Azure and GCP jobs without modification.

---

## 2. AWS (already implemented — reference)

```text
internal/cloud/aws/
    credentials/    FromSession (deprecated), Validate, HomeRegion
    ssm/            Executor implements RemoteExecutor
    ec2/            ListRunning
    vpc/            Describe
```

Credential input: Access Key ID + Secret + Session Token (temp STS creds).

---

## 3. Azure

### 3.1 Remote execution

Azure Run Command (`az vm run-command invoke`) is the equivalent of SSM
SendCommand. The Azure SDK provides this via the `armcompute` package.

```text
internal/cloud/azure/
    credentials/    Azure credential management (service principal or device flow)
    runcommand/     Executor implements RemoteExecutor via az vm run-command
    compute/        ListRunning — equivalent of ec2.ListRunning
    vnet/           Describe — VNet, subnets, NSGs
```

#### Run Command behaviour differences from SSM

| SSM | Azure Run Command |
|---|---|
| Async: returns commandID immediately | Sync by default; async via `--no-wait` + operation ID |
| Poll via GetCommandInvocation | Poll via operation status URI returned in response header |
| 2592000s max timeout | No explicit command timeout; VM agent handles it |
| Instance ID format: `i-xxxxxxxxxxxxxxxxx` | VM resource ID: `/subscriptions/.../virtualMachines/name` |

The `Send()` implementation for Azure submits the run command with `--no-wait`
and returns the operation ID as the `commandID`. `WaitForDone()` polls the
operation status URI.

#### Instance ID convention

Azure VM resource IDs are long strings. The `ExecutionBatch` and `Execution`
models store them in the existing `instance_id` column — no schema change
needed, the column is a plain string.

### 3.2 Credential model

Azure uses one of:
- **Service principal**: `AZURE_CLIENT_ID` + `AZURE_CLIENT_SECRET` + `AZURE_TENANT_ID`
- **Device flow** (interactive, appropriate for TUI use): user authenticates
  via browser on their workstation, token cached locally

For the TUI credential input screen, the simplest approach for bastion use is
service principal credentials (three values, same input pattern as AWS).
Device flow is a stretch goal.

### 3.3 Cloud-specific tools

| Tool | Scope | What it calls |
|---|---|---|
| VNet Recon | cloud/azure | `armnetwork` — VirtualNetworks, Subnets, NetworkSecurityGroups |
| Subscription Accounts | cloud/azure | `armsubscriptions` — list accessible subscriptions |
| Management Group Traversal | cloud/azure | `armmanagementgroups` — equivalent of AWS org traversal |

`Tool.scope = "cloud"` with a `provider` field (Phase 3 addition) set to
`"azure"` to gate visibility in the TUI.

---

## 4. GCP

### 4.1 Remote execution

GCP OS Config / VM Manager provides `RunCommand` equivalent via the Compute
Engine API (`compute.instances.aggregatedList` for discovery,
`osconfig.projects.locations.instances.runCommand` for execution — or
`gcloud compute ssh --command` for simpler use cases).

The more production-appropriate approach is **GCP OS Config agent** with
`RunCommand`, which mirrors the SSM pattern closely.

```text
internal/cloud/gcp/
    credentials/    GCP credential management (service account JSON or ADC)
    runcommand/     Executor implements RemoteExecutor via OS Config RunCommand
    compute/        ListRunning — Compute Engine instance listing
    vpc/            Describe — VPC networks, subnets, firewall rules
```

#### Run Command behaviour differences

| SSM | GCP RunCommand |
|---|---|
| Async, commandID returned immediately | Operation resource returned immediately |
| Poll GetCommandInvocation | Poll operation via `operations.get()` |
| Instance ID: `i-xxxxxxxxxxxxxxxxx` | Instance name or `zones/{zone}/instances/{name}` |
| Requires SSM agent on instance | Requires OS Config agent on instance |

### 4.2 Credential model

GCP uses Application Default Credentials (ADC). For bastion use, the system
user running the TUI would have a service account JSON key configured.

TUI credential input: path to service account JSON file, or use ADC from
environment automatically.

### 4.3 Cloud-specific tools

| Tool | Scope | What it calls |
|---|---|---|
| VPC Recon | cloud/gcp | Compute Engine — networks, subnetworks, firewall rules |
| Project Accounts | cloud/gcp | Resource Manager — list projects in org/folder |
| Org Traversal | cloud/gcp | Resource Manager — folders and projects tree |

---

## 5. Tool model extension for Phase 3

In Phase 2, `Tool.scope` is `"os"` or `"cloud"`. In Phase 3, cloud-specific
tools need a `provider` tag so the TUI knows which provider's credentials are
required to enable them.

Options:

**Option A** — Compound scope string: `"cloud/aws"`, `"cloud/azure"`, `"cloud/gcp"`
Simple, no schema change beyond what's already scoped in Phase 1.

**Option B** — Separate `Provider` field: `provider = "aws" | "azure" | "gcp" | ""`
More explicit, easier to query.

Recommended: **Option B**. Add `Provider string` to the `Tool` model in the
Phase 3 migration. Empty string means "all providers" (applies to `scope = "os"`
tools). Cloud tools set the appropriate provider value.

```go
type Tool struct {
    // ... existing fields ...
    Scope    string `gorm:"not null;default:os" json:"scope"`
    Provider string `gorm:"default:''" json:"provider"` // "", "aws", "azure", "gcp"
}
```

---

## 6. TUI changes for Phase 3

The Cloud Tools screen (Phase 2, section 3.4) already renders provider
sections as stubs:

```text
Azure    ○  (coming soon)
GCP      ○  (coming soon)
```

In Phase 3:
- Stub labels are replaced with live status indicators
- Credential input modal gets environment type selector: `AWS` | `Azure` | `GCP`
- Each provider's credential input fields differ (shown conditionally)
- Instance selector optionally aggregates across all authenticated providers

---

## 7. Shared concerns across all providers

### Script content

Scripts are already provider-agnostic — `script_type` (bash/powershell) and
`platform` (linux/windows) are the only dimensions. A bash script that checks
disk usage runs identically on an AWS, Azure, or GCP Linux instance regardless
of which remote execution agent delivers it.

CRLF normalisation in `Send()` applies to all providers.

### Execution records

`Execution.account_id` and `Execution.region` are AWS-centric names but are
used as free-form strings. For Azure and GCP:
- `account_id` → subscription ID (Azure) or project ID (GCP)
- `region` → Azure region or GCP zone/region

No schema change needed; the semantics are "identifier of the cloud account
unit" and "identifier of the geographic deployment location."

### DB schema

No new tables for Phase 3. The existing `Execution`, `ExecutionBatch`,
`Script`, and `Tool` models accommodate all three providers.

---

## 8. Sequencing within Phase 3

Recommended order:

1. Azure credentials + RunCommand executor (unblocks OS tools on Azure)
2. Azure compute listing (instance selector)
3. Azure cloud tools (VNet recon, subscription list)
4. GCP credentials + RunCommand executor
5. GCP compute listing
6. GCP cloud tools (VPC recon, project list)
7. TUI multi-provider credential screen
8. TUI instance selector cross-cloud aggregation

Steps 1–2 can be done independently of steps 4–5. The TUI changes (7–8)
follow both providers having working executors.

---

## 9. Acceptance criteria

- [ ] `internal/cloud/azure/runcommand` implements `exec.RemoteExecutor`
- [ ] `internal/cloud/gcp/runcommand` implements `exec.RemoteExecutor`
- [ ] Existing `exec.Runner` runs jobs on Azure and GCP instances without
  modification to runner logic
- [ ] Azure VNet recon returns VNets, subnets, NSGs in the same structure as
  AWS VPC recon (normalised to a shared `NetworkDescription` type)
- [ ] GCP VPC recon same
- [ ] TUI Cloud Tools screen shows Azure and GCP sections with live status
- [ ] OS tools (patching QC, disk recon etc.) run successfully on Linux
  instances across all three providers in integration testing
- [ ] `go test ./...` passes; all existing AWS tests unaffected
