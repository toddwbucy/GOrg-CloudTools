package exec

import (
	"context"

	"github.com/toddwbucy/GOrg-CloudTools/internal/cloud/aws/ssm"
)

// RemoteExecutor is the cloud-agnostic interface for running a script on a
// remote instance and waiting for the result. AWS SSM, Azure RunCommand, and
// GCP RunCommand all implement this interface.
//
// Defined at the point of use (exec package) so tests can inject a mock
// without importing a real cloud SDK client.
type RemoteExecutor interface {
	Send(ctx context.Context, instanceIDs []string, script, platform string) (string, error)
	WaitForDone(ctx context.Context, commandID, instanceID string) (*ssm.InvocationStatus, error)
}
