# Deferred Integration Tests

This document tracks functions and API handler paths whose tests are intentionally
deferred. All entries require live AWS credentials against a **dedicated, non-client
test account**. They must not be run against a client AWS organization.

When a safe test account is available, these should be implemented as build-tag-gated
integration tests:

```bash
go test -tags integration ./...
```

---

## Reason for deferral

The AWS organization currently accessible is a **production client environment**.
Running live AWS API calls (SSM SendCommand, EC2 DescribeInstances, STS
GetCallerIdentity, etc.) against it during development carries unacceptable risk.
All functions listed below are thin wrappers over AWS SDK calls with no logic
worth testing independently of the real API.

---

## Deferred by package

### `internal/aws/ssm`

| Function | Reason |
|---|---|
| `(*Executor).Send` | Issues SSM `SendCommand` against a real regional endpoint |
| `(*Executor).GetStatus` | Calls `GetCommandInvocation`; requires a real command ID |
| `(*Executor).Run` | Wraps `Send` + `pollUntilDone`; end-to-end SSM execution |
| `(*Executor).WaitForDone` | Wraps `pollUntilDone`; requires a live in-flight command |

The pure-logic helpers (`isTerminal`, `isRetryableSSMError`, `New` timeout
clamping) are already unit-tested in `executor_test.go`.

---

### `internal/aws/ec2`

| Function | Reason |
|---|---|
| `ListRunning` | Calls EC2 `DescribeInstances` paginator against a real regional endpoint |

The pure-logic helpers (`normalizePlatform`, `nameTag`) are already unit-tested
in `instances_test.go`.

---

### `internal/aws/vpc`

| Function | Reason |
|---|---|
| `Describe` | Calls EC2 `DescribeVpcs`, `DescribeSubnets`, and `DescribeSecurityGroups` against a real regional endpoint |

The pure-logic helper (`tagsToMap`) is already unit-tested in `vpc_test.go`.

---

### `internal/aws/credentials`

| Function | Reason |
|---|---|
| `Validate` | Calls STS `GetCallerIdentity`; requires credentials that are valid against a real AWS account |

`HomeRegion` and `FromSession` (config build + error paths) are already
unit-tested in `manager_test.go`.

---

### `internal/exec`

| Type / Function | Reason |
|---|---|
| `OrgRunner` (all methods) | Wraps `*gorgaws.OrgVisitor`, a concrete struct with no injectable interface. Cannot be mocked without either live AWS management credentials or a production code change to introduce a visitor interface. |

`Runner` and its `start` seam are already fully unit-tested in `runner_test.go`
via the `SSMExecutor` mock interface.

---

### `internal/api` — handler happy paths

The API handler tests in `internal/api/*_test.go` cover validation (400),
auth-guard (401/503), and all DB-only paths. The following handler **success
paths** are untested because they call through to real AWS:

| Handler | Endpoint | What it calls |
|---|---|---|
| `handleExecScript` | `POST /api/exec/script` | `exec.Runner.Start` → SSM `SendCommand` |
| `handleExecOrgScript` | `POST /api/exec/org-script` | `exec.OrgRunner.Start` → gorg-aws + SSM |
| `handleGetCommandStatus` | `GET /api/aws/ssm/commands/{id}/status` | SSM `GetCommandInvocation` |
| `handleListInstances` | `GET /api/aws/instances` | EC2 `DescribeInstances` |
| `handleDescribeVPCs` | `GET /api/aws/vpcs` | EC2 `DescribeVpcs/Subnets/SecurityGroups` |
| `handleOrgAccounts` | `GET /api/aws/org/accounts` | `OrgRunner.DryRun` → gorg-aws |
| `handleCreateCredentials` (non-dev) | `POST /api/auth/aws-credentials` | STS `GetCallerIdentity` |

---

## Prerequisites for running integration tests

1. A dedicated AWS test account (not a client account).
2. IAM credentials with at minimum: `ssm:SendCommand`, `ssm:GetCommandInvocation`,
   `ec2:DescribeInstances`, `ec2:DescribeVpcs`, `ec2:DescribeSubnets`,
   `ec2:DescribeSecurityGroups`, `sts:GetCallerIdentity`.
3. At least one running EC2 instance with SSM agent installed in the test account.
4. For `OrgRunner` tests: management credentials that can assume
   `OrganizationAccountAccessRole` across the test org.

Set credentials via the standard environment variables (`AWS_ACCESS_KEY_ID_COM`,
`AWS_SECRET_ACCESS_KEY_COM`, etc.) and run:

```bash
go test -tags integration -timeout 10m ./...
```
