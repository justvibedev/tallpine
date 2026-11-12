package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/runfile"
)

const defaultStopTimeout = 10 * time.Second

// stopAgentsTimeout is the default wait when the daemon also stops every
// agent: each provider gets a few seconds to exit cleanly.
const stopAgentsTimeout = 45 * time.Second

type stopOptions struct {
	timeout    time.Duration
	json       bool
	stopAgents bool
}

func newStopCommand(ctx *commandContext) *cobra.Command {
	opts := stopOptions{timeout: defaultStopTimeout}
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the " + brand.ProductName + " daemon",
		Args:  noArgs,
		Long: "Stop the " + brand.ProductName + " daemon. Agents keep running and the next daemon adopts them,\n" +
			"as a restart does. With --stop-agents the daemon first stops every agent's processes;\n" +
			"each session sleeps and resumes its conversation on its next message.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.stopAgents && !cmd.Flags().Changed("timeout") {
				opts.timeout = stopAgentsTimeout
			}
			st, err := ctx.stopDaemon(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), st)
			}
			if st.State == stateStopped {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), brand.ProductName+" daemon stopped")
				return err
			}
			return writeStatus(cmd, st)
		},
	}
	cmd.Flags().DurationVar(&opts.timeout, "timeout", defaultStopTimeout, "How long to wait for daemon shutdown")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output stop result as JSON")
	cmd.Flags().BoolVar(&opts.stopAgents, "stop-agents", false, "Also stop every agent's processes; sessions sleep and resume on their next message")
	return cmd
}

func (c *commandContext) stopDaemon(ctx context.Context, opts stopOptions) (daemonStatus, error) {
	cfg, err := config.Load()
	if err != nil {
		return daemonStatus{}, err
	}
	st, err := c.inspectDaemon(ctx)
	if err != nil {
		return daemonStatus{}, err
	}
	switch st.State {
	case stateStopped:
		return st, nil
	case stateStale:
		if err := runfile.Remove(cfg.RunFilePath); err != nil {
			return daemonStatus{}, err
		}
		return daemonStatus{State: stateStopped, RunFile: cfg.RunFilePath, DataDir: cfg.DataDir}, nil
	}
	if !st.owned {
		if st.Error != "" {
			return daemonStatus{}, fmt.Errorf("daemon pid %d is alive but ownership could not be verified: %s", st.PID, st.Error)
		}
		return daemonStatus{}, fmt.Errorf("daemon pid %d is alive but ownership could not be verified", st.PID)
	}

	if err := c.requestShutdown(ctx, st.Socket, st.Port, cfg.RunFilePath, opts.stopAgents); err != nil {
		if !isPeerNotAdmitted(err) {
			return daemonStatus{}, fmt.Errorf("request daemon shutdown: %w", err)
		}
		if err := c.terminateRefused(st.PID, opts.stopAgents); err != nil {
			return daemonStatus{}, err
		}
	}
	return c.waitForStopped(ctx, st.PID, cfg.RunFilePath, cfg.DataDir, opts.timeout)
}

// terminateRefused stops a daemon whose API refused this process (B631): the
// owner's recovery when the desktop app cannot start. SIGTERM takes the same
// shutdown path as POST /shutdown, so agents keep running. pid is the one
// /healthz confirmed. Stopping agents needs the API, so --stop-agents cannot
// fall back.
func (c *commandContext) terminateRefused(pid int, stopAgents bool) error {
	if stopAgents {
		return fmt.Errorf("the daemon refused this process, and --stop-agents needs its API; stop agents from the %s desktop app, or run %s stop without --stop-agents", brand.ProductName, brand.CLIName)
	}
	if c.deps.Terminate == nil {
		return errors.New("the daemon refused this process and no way to signal it is configured")
	}
	_, _ = fmt.Fprintf(c.deps.Err, "the daemon refused this process; stopping pid %d with SIGTERM (agents keep running)\n", pid)
	if err := c.deps.Terminate(pid); err != nil {
		return fmt.Errorf("signal daemon pid %d: %w", pid, err)
	}
	return nil
}

// terminateProcess sends SIGTERM to pid.
func terminateProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGTERM)
}

func (c *commandContext) requestShutdown(ctx context.Context, socket string, port int, runFilePath string, stopAgents bool) error {
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	var body io.Reader = http.NoBody
	if stopAgents {
		body = strings.NewReader(`{"stopAgents":true}`)
	}
	base, client := c.daemonEndpoint(socket, port, c.deps.HTTPClient.Timeout)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+"/shutdown", body)
	if err != nil {
		return err
	}
	setDaemonToken(req, runFilePath)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body apiError
		_ = json.NewDecoder(io.LimitReader(resp.Body, maxDrainedBodyBytes)).Decode(&body)
		return apiResponseError{StatusCode: resp.StatusCode, ErrorBody: body}
	}
	return nil
}

func (c *commandContext) waitForStopped(ctx context.Context, pid int, runFilePath, dataDir string, timeout time.Duration) (daemonStatus, error) {
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}
	deadline := c.deps.Now().Add(timeout)
	for {
		select {
		case <-ctx.Done():
			return daemonStatus{}, ctx.Err()
		default:
		}

		info, err := runfile.Read(runFilePath)
		if err != nil {
			return daemonStatus{}, err
		}
		alive := c.deps.ProcessAlive(pid)
		if !alive {
			// Only remove the run-file if it still belongs to the process we
			// stopped. A concurrent `tallpine start` may have already written a new
			// run-file for a different daemon; removing that would corrupt its
			// handshake and make a live daemon look stopped.
			if info != nil && info.PID == pid {
				if err := runfile.Remove(runFilePath); err != nil {
					return daemonStatus{}, err
				}
			}
			return daemonStatus{State: stateStopped, RunFile: runFilePath, DataDir: dataDir}, nil
		}
		if info == nil {
			// The run-file is the daemon's own liveness marker; it removes it as
			// it shuts down, before the OS process has necessarily exited. Once
			// the marker is gone the daemon has committed to stopping, so treat
			// that as stopped.
			//
			// We still poll for full process exit as a best effort so Windows
			// releases inherited handles such as daemon.log before callers clean
			// up the data directory, but exceeding the timeout is NOT an error:
			// with no desktop client connected the daemon can drain its
			// background workers slower than the stop timeout, and failing here
			// made `tallpine stop` spuriously report failure (issue #2214).
			if !c.deps.Now().Before(deadline) {
				return daemonStatus{State: stateStopped, RunFile: runFilePath, DataDir: dataDir}, nil
			}
			c.deps.Sleep(100 * time.Millisecond)
			continue
		}
		if !c.deps.Now().Before(deadline) {
			return daemonStatus{}, fmt.Errorf("daemon pid %d did not stop within %s", pid, timeout)
		}
		c.deps.Sleep(100 * time.Millisecond)
	}
}
