package exec

import "context"

// InvocationResult is the cloud-agnostic result of a remote script execution.
// All cloud providers translate their native status type to this struct so that
// exec.Runner and exec.OrgRunner never depend on provider-specific packages.
type InvocationResult struct {
	CommandID  string
	InstanceID string
	// Status mirrors the provider's terminal state name. Canonical values used
	// across all providers: "Success", "Failed", "TimedOut", "Cancelled".
	Status   string
	Output   string // stdout
	Error    string // stderr
	ExitCode int
	Done     bool // true when the command has reached a terminal state
}

// RemoteExecutor is the cloud-agnostic interface for sending a script to a
// remote instance and waiting for the result. AWS SSM, Azure RunCommand, and
// GCP RunCommand all satisfy this interface via provider-specific adapters
// defined in this package.
//
// Defined here (exec package) so tests can inject a mock without importing any
// real cloud SDK client. Provider adapters live alongside their cloud packages
// but are registered in this package to avoid circular imports.
type RemoteExecutor interface {
	Send(ctx context.Context, instanceIDs []string, script, platform string) (string, error)
	WaitForDone(ctx context.Context, commandID, instanceID string) (*InvocationResult, error)
}
