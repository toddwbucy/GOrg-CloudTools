// Package gcp will contain GCP-specific execution and discovery primitives.
// Implementation is deferred to Phase 3 (multi-cloud expansion).
//
// Planned sub-packages:
//
//	credentials/  — GCP service account JSON / Application Default Credentials
//	runcommand/   — Executor implementing exec.RemoteExecutor via OS Config RunCommand
//	compute/      — ListRunning — Compute Engine instance listing
//	vpc/          — Describe — VPC networks, subnets, firewall rules
package gcp
