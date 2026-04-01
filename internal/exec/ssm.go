package exec

import (
	"context"

	"github.com/toddwbucy/GOrg-CloudTools/internal/aws/ssm"
)

// SSMExecutor is the interface consumed by Runner for sending and polling
// SSM commands. Defined at the point of use so tests can inject a mock
// without importing the real AWS SSM client. The concrete *ssm.Executor
// satisfies this interface automatically.
type SSMExecutor interface {
	Send(ctx context.Context, instanceIDs []string, script, platform string) (string, error)
	WaitForDone(ctx context.Context, commandID, instanceID string) (*ssm.InvocationStatus, error)
}
