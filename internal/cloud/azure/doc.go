// Package azure will contain Azure-specific execution and discovery primitives.
// Implementation is deferred to Phase 3 (multi-cloud expansion).
//
// Planned sub-packages:
//
//	credentials/  — Azure service principal and device flow credential management
//	runcommand/   — Executor implementing exec.RemoteExecutor via az vm run-command
//	compute/      — ListRunning — equivalent of cloud/aws/ec2.ListRunning
//	vnet/         — Describe — VNet, subnets, NSGs
package azure
