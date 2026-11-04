package previewserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type persistedProcess struct {
	SessionID domain.SessionID `json:"sessionId"`
	PID       int              `json:"pid"`
	Port      int              `json:"port"`
	StartedAt time.Time        `json:"startedAt"`
	// StartTime is the kernel-reported start time of PID at launch. A PID from
	// a previous daemon generation is stale by construction; reap refuses to
	// kill unless the live process still carries this identity.
	StartTime string `json:"startTime,omitempty"`
}

func (m *Manager) reapPersistedProcesses() {
	data, err := os.ReadFile(m.registryPath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		m.log.Warn("read preview process registry", "err", err)
		return
	}
	var processes []persistedProcess
	if err := json.Unmarshal(data, &processes); err != nil {
		m.log.Warn("decode preview process registry", "err", err)
		return
	}
	for _, process := range processes {
		if process.PID <= 0 {
			continue
		}
		// PIDs recorded by a previous daemon generation are stale by
		// construction: the number may have been recycled by anything since
		// (one recycled group kill destroyed a whole tmux server, #3475).
		// Kill only what still carries the recorded launch identity.
		if process.StartTime == "" {
			m.log.Warn("skip reaping preview process without a recorded start time",
				"session", process.SessionID, "pid", process.PID)
			continue
		}
		if err := forceKillPreviewPID(process.PID, process.StartTime); err != nil {
			m.log.Warn("reap orphaned preview process", "session", process.SessionID, "pid", process.PID, "err", err)
		}
	}
	if err := os.Remove(m.registryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.log.Warn("remove stale preview process registry", "err", err)
	}
}

func (m *Manager) persistProcessesLocked() {
	if m.registryPath == "" {
		return
	}
	processes := make([]persistedProcess, 0, len(m.runs))
	for id, run := range m.runs {
		if run.cmd == nil || run.cmd.Process == nil {
			continue
		}
		processes = append(processes, persistedProcess{
			SessionID: id,
			PID:       run.cmd.Process.Pid,
			Port:      run.status.Port,
			StartedAt: run.status.StartedAt,
			StartTime: run.startTime,
		})
	}
	if len(processes) == 0 {
		_ = os.Remove(m.registryPath)
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.registryPath), 0o700); err != nil {
		m.log.Warn("create preview process registry directory", "err", err)
		return
	}
	data, err := json.MarshalIndent(processes, "", "  ")
	if err != nil {
		m.log.Warn("encode preview process registry", "err", err)
		return
	}
	if err := os.WriteFile(m.registryPath, append(data, '\n'), 0o600); err != nil {
		m.log.Warn("write preview process registry", "err", err)
	}
}
