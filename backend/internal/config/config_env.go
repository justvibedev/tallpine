package config

import (
	"fmt"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// loadLimits reads the port, timeout and artifact-size variables.
func (cfg *Config) loadLimits() error {
	if raw := brand.Getenv("PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("invalid TALLPINE_PORT %q: %w", raw, err)
		}
		if port < 1 || port > 65535 {
			return fmt.Errorf("invalid TALLPINE_PORT %d: out of range 1-65535", port)
		}
		cfg.Port = port
	}

	if raw := brand.Getenv("REQUEST_TIMEOUT"); raw != "" {
		d, err := parsePositiveDuration("TALLPINE_REQUEST_TIMEOUT", raw)
		if err != nil {
			return err
		}
		cfg.RequestTimeout = d
	}

	if raw := brand.Getenv("SPAWN_TIMEOUT"); raw != "" {
		d, err := parsePositiveDuration("TALLPINE_SPAWN_TIMEOUT", raw)
		if err != nil {
			return err
		}
		cfg.SpawnTimeout = d
	}

	// The tmux adapter reads this variable itself and would silently fall back to
	// its default on a bad value; reject it here like the other timeouts.
	if raw := strings.TrimSpace(brand.Getenv("TMUX_COMMAND_TIMEOUT")); raw != "" {
		if _, err := parsePositiveDuration("TALLPINE_TMUX_COMMAND_TIMEOUT", raw); err != nil {
			return err
		}
	}

	if raw := brand.Getenv("SHUTDOWN_TIMEOUT"); raw != "" {
		d, err := parsePositiveDuration("TALLPINE_SHUTDOWN_TIMEOUT", raw)
		if err != nil {
			return err
		}
		cfg.ShutdownTimeout = d
	}

	if raw := strings.TrimSpace(brand.Getenv("ARTIFACT_VERSIONS_KEEP")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("invalid TALLPINE_ARTIFACT_VERSIONS_KEEP %q: %w", raw, err)
		}
		if n < 1 || n > MaxArtifactVersionsKeep {
			return fmt.Errorf("invalid TALLPINE_ARTIFACT_VERSIONS_KEEP %d: out of range 1-%d", n, MaxArtifactVersionsKeep)
		}
		cfg.ArtifactVersionsKeep = n
	}

	if raw := strings.TrimSpace(brand.Getenv("ARTIFACT_SIZE_WARN_MB")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid TALLPINE_ARTIFACT_SIZE_WARN_MB %q: %w", raw, err)
		}
		if n < 1 || n > 1<<30 {
			return fmt.Errorf("invalid TALLPINE_ARTIFACT_SIZE_WARN_MB %d: out of range 1-%d", n, 1<<30)
		}
		cfg.ArtifactSizeWarnBytes = n << 20
	}
	return nil
}

// loadRuntimeToggles reads the origin, update and review-timing variables.
func (cfg *Config) loadRuntimeToggles() error {
	if raw, ok := brand.LookupEnv("ALLOWED_ORIGINS"); ok && raw != "" {
		// Explicit override replaces the defaults entirely so a deployment can
		// also narrow the list. The "null" origin is rejected, never silently
		// dropped: an operator allowing it would open the no-auth daemon to
		// every sandboxed iframe on the web.
		origins := make([]string, 0, 4)
		for _, origin := range strings.Split(raw, ",") {
			origin = strings.TrimSpace(origin)
			if origin == "" {
				continue
			}
			if origin == "null" || origin == "*" {
				return fmt.Errorf("invalid TALLPINE_ALLOWED_ORIGINS entry %q: wildcard and null origins are not allowed", origin)
			}
			origins = append(origins, origin)
		}
		cfg.AllowedOrigins = origins
	}

	if raw, present := brand.LookupEnv("HARNESS_UPDATE_CHECKS"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("TALLPINE_HARNESS_UPDATE_CHECKS", raw)
		if err != nil {
			return err
		}
		cfg.HarnessUpdateChecks = v
	}
	if raw, present := brand.LookupEnv("LOOPBACK_TCP"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("TALLPINE_LOOPBACK_TCP", raw)
		if err != nil {
			return err
		}
		cfg.LoopbackTCP = v
	}
	if raw, present := brand.LookupEnv("UPDATE_WHILE_RUNNING"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("TALLPINE_UPDATE_WHILE_RUNNING", raw)
		if err != nil {
			return err
		}
		cfg.UpdateWhileRunning = v
	}
	if raw, present := brand.LookupEnv("VERSION_REFRESH"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("TALLPINE_VERSION_REFRESH", raw)
		if err != nil {
			return err
		}
		cfg.VersionRefresh = v
	}
	if raw, present := brand.LookupEnv("TUI_VERSION_REFRESH"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("TALLPINE_TUI_VERSION_REFRESH", raw)
		if err != nil {
			return err
		}
		cfg.TUIVersionRefresh = v
	}
	if raw, present := brand.LookupEnv("SESSION_GRADLE_HOME"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("TALLPINE_SESSION_GRADLE_HOME", raw)
		if err != nil {
			return err
		}
		cfg.SessionGradleHome = v
	}
	// The older Windows-only name is read first so the current one wins.
	for _, suffix := range []string{"WINDOWS_UPDATE_WHEN_IDLE", "UPDATE_WHEN_IDLE"} {
		if raw, present := brand.LookupEnv(suffix); present && strings.TrimSpace(raw) != "" {
			v, err := parseToggleEnv("TALLPINE_"+suffix, raw)
			if err != nil {
				return err
			}
			cfg.UpdateWhenIdle = v
		}
	}
	if raw, present := brand.LookupEnv("MANAGED_HARNESS_INSTALLS"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("TALLPINE_MANAGED_HARNESS_INSTALLS", raw)
		if err != nil {
			return err
		}
		cfg.ManagedHarnessInstalls = v
	}
	if raw, present := brand.LookupEnv("VERSION_REFRESH_IDLE"); present && strings.TrimSpace(raw) != "" {
		d, err := parsePositiveDuration("TALLPINE_VERSION_REFRESH_IDLE", strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		cfg.VersionRefreshIdle = d
	}
	if raw, present := brand.LookupEnv("WORKER_IDLE_NUDGE"); present && strings.TrimSpace(raw) != "" {
		d, err := parseOffOrDuration("TALLPINE_WORKER_IDLE_NUDGE", strings.TrimSpace(raw), MinWorkerIdleNudge)
		if err != nil {
			return err
		}
		cfg.WorkerIdleNudge = d
	}
	if raw, present := brand.LookupEnv("REVIEW_PASS_DEADLINE"); present && strings.TrimSpace(raw) != "" {
		d, err := parseOffOrDuration("TALLPINE_REVIEW_PASS_DEADLINE", strings.TrimSpace(raw), MinReviewPassDeadline)
		if err != nil {
			return err
		}
		cfg.ReviewPassDeadline = d
	}
	if raw, present := brand.LookupEnv("REVIEW_IDLE_GRACE"); present && strings.TrimSpace(raw) != "" {
		d, err := parseOffOrDuration("TALLPINE_REVIEW_IDLE_GRACE", strings.TrimSpace(raw), MinReviewIdleGrace)
		if err != nil {
			return err
		}
		cfg.ReviewIdleGrace = d
	}
	if raw, present := brand.LookupEnv("HARNESS_UPDATE_INTERVAL"); present && strings.TrimSpace(raw) != "" {
		d, err := parsePositiveDuration("TALLPINE_HARNESS_UPDATE_INTERVAL", strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		if d < MinHarnessUpdateInterval {
			return fmt.Errorf("invalid TALLPINE_HARNESS_UPDATE_INTERVAL %q: must be at least %s", raw, MinHarnessUpdateInterval)
		}
		cfg.HarnessUpdateInterval = d
	}
	return nil
}

// loadFeatureFlags reads the tracker, backlog, planner and offering switches.
func (cfg *Config) loadFeatureFlags() error {
	if raw, ok := brand.LookupEnv("TRACKER_INTAKE"); ok && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("TALLPINE_TRACKER_INTAKE", raw)
		if err != nil {
			return err
		}
		cfg.TrackerIntake = v
	}

	cfg.BacklogRun = true
	if raw, ok := brand.LookupEnv("BACKLOG_RUN"); ok && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("TALLPINE_BACKLOG_RUN", raw)
		if err != nil {
			return err
		}
		cfg.BacklogRun = v
	}

	cfg.Planner = true
	if raw, ok := brand.LookupEnv("PLANNER"); ok && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("TALLPINE_PLANNER", raw)
		if err != nil {
			return err
		}
		cfg.Planner = v
	}

	if raw := brand.Getenv("CLIENT"); raw != "" {
		cfg.Client = strings.TrimSpace(raw)
	}
	if raw := brand.Getenv("LOCAL_OFFERING"); raw != "" {
		v, err := parseToggleEnv("TALLPINE_LOCAL_OFFERING", raw)
		if err != nil {
			return err
		}
		cfg.LocalOffering = v
	}
	return nil
}
