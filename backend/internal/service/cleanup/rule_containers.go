package cleanup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// containerGrace leaves a just-ended session's containers to the reap that
// runs on termination.
const containerGrace = 10 * time.Minute

// ContainerInventory lists and removes session-labeled Docker resources.
type ContainerInventory interface {
	SessionContainers(ctx context.Context) ([]ports.LabeledResource, error)
	SessionVolumes(ctx context.Context) ([]ports.LabeledResource, error)
	RemoveContainer(ctx context.Context, id string) error
	RemoveVolume(ctx context.Context, name string) error
}

// ProjectLister lists registered projects and re-reads a session right
// before one of its resources is removed.
type ProjectLister interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// ContainerRule removes Docker containers and volumes labeled tallpine.session=<id>
// whose session has ended: the termination-time reap missed them (the daemon
// was down, or Docker was). It keeps anything labeled tallpine.spare=true, anything
// of a project that opted out of container reaping, anything whose session
// this install does not know (another data directory may own it), and
// anything of a live session; each is reported with its reason. Volumes are
// kept for VolumeRetentionDays. It is off by default: until the user turns it
// on, runs only report what it would remove.
type ContainerRule struct {
	docker   ContainerInventory
	projects ProjectLister
}

// NewContainerRule builds the rule.
func NewContainerRule(docker ContainerInventory, projects ProjectLister) *ContainerRule {
	return &ContainerRule{docker: docker, projects: projects}
}

// Category implements Rule.
func (*ContainerRule) Category() Category { return CategoryContainers }

// Light implements Rule.
func (*ContainerRule) Light() bool { return true }

// Enabled implements Rule: the rule always runs, and only removes anything
// once the policy turns it on; while off it reports what it would remove.
func (*ContainerRule) Enabled(Policy) bool { return true }

// Run implements Rule.
func (r *ContainerRule) Run(ctx context.Context, run *Run) error {
	if r.docker == nil || r.projects == nil {
		return errors.New("container cleanup is not configured")
	}
	containers, err := r.docker.SessionContainers(ctx)
	if err != nil {
		return err
	}
	volumes, err := r.docker.SessionVolumes(ctx)
	if err != nil {
		return err
	}
	if len(containers)+len(volumes) == 0 {
		return nil
	}
	projects, err := r.projects.ListProjects(ctx)
	if err != nil {
		return err
	}
	optedOut := map[domain.ProjectID]bool{}
	for _, p := range projects {
		optedOut[domain.ProjectID(p.ID)] = p.Config.ContainerReap.Disabled
	}
	byID := map[domain.SessionID]domain.SessionRecord{}
	for _, rec := range run.Sessions {
		byID[rec.ID] = rec
	}
	volumeRetention := time.Duration(run.Policy.Containers.VolumeRetentionDays) * 24 * time.Hour
	judge := func(res ports.LabeledResource, what string, retention time.Duration, remove func(context.Context, string) error) {
		rec, known := byID[domain.SessionID(res.Session)]
		name := res.Name
		if name == "" {
			name = res.ID
		}
		item := Item{SessionID: domain.SessionID(res.Session), ProjectID: rec.ProjectID, What: what + " " + name, Status: StatusKept}
		switch {
		case res.Spare:
			item.Reason = "labeled tallpine.spare=true"
		case known && !rec.IsTerminated:
			item.Reason = "its session is live"
		case !known:
			item.Reason = "its session is unknown to this install"
		case rec.ProjectID != "" && optedOut[rec.ProjectID]:
			item.Reason = "the project opted out of container reaping"
		case run.Now.Sub(rec.UpdatedAt) < retention:
			item.Reason = fmt.Sprintf("its session ended less than %s ago", retention.Round(time.Minute))
		case !run.Policy.Containers.Enabled:
			item.Status = StatusNeedsConfirmation
			item.Reason = "container cleanup is off; turn on containers to remove these"
		case run.DryRun:
			item.Status = StatusWouldFree
		default:
			if cur, found, err := r.projects.GetSession(ctx, item.SessionID); err != nil {
				item.Reason = "its session could not be re-read"
				break
			} else if found && !cur.IsTerminated {
				item.Reason = "its session is live again"
				break
			}
			if err := remove(ctx, res.ID); err != nil {
				item.Status, item.Reason = StatusFailed, err.Error()
			} else {
				item.Status = StatusFreed
			}
			_ = run.Pace(ctx)
		}
		run.Add(item)
	}
	// Containers first, so a volume they used is free to go.
	for _, c := range containers {
		judge(c, "container", containerGrace, r.docker.RemoveContainer)
	}
	for _, v := range volumes {
		judge(v, "volume", max(volumeRetention, containerGrace), r.docker.RemoveVolume)
	}
	return ctx.Err()
}
