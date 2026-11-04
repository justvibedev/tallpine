package daemonmeta

import "tallpine.eu/tallpine/backend/internal/brand"

// ServiceName identifies the daemon in loopback health/readiness probes.
// The CLI uses it with the reported PID to avoid signaling an unrelated process
// when a stale run-file's PID has been reused. It is brand-specific so a probe
// never mistakes an official AO daemon for ours.
const ServiceName = brand.DaemonServiceName
