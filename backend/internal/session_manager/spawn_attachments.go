package sessionmanager

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/attachmentstore"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// attachmentsDir is the worktree-relative directory where spawn file
// attachments are projected for agents.
const attachmentsDir = attachmentstore.WorkspaceDir

// writeSpawnAttachments writes canonical and worktree-projected copies named
// attachment-1<ext>, attachment-2<ext>, ... and returns the worktree-relative
// paths in order. The projections are excluded from git via info/exclude.
func (m *Manager) writeSpawnAttachments(ctx context.Context, id domain.SessionID, workspacePath string, attachments []ports.SpawnAttachment) ([]string, error) {
	refs := spawnAttachmentRefs(attachments)
	for i, a := range attachments {
		if err := m.attachments.Put(ctx, id, workspacePath, spawnAttachmentName(i, a), a.Data); err != nil {
			return nil, fmt.Errorf("write attachment %d: %w", i+1, err)
		}
	}
	return refs, nil
}

func spawnAttachmentName(index int, attachment ports.SpawnAttachment) string {
	ext := attachment.Ext
	if ext == "" {
		ext = ".bin"
	}
	return fmt.Sprintf("attachment-%d%s", index+1, ext)
}

// spawnAttachmentRefs names the attachments as the prompt will reference them.
// The reference is worktree-relative and derived only from the attachment's
// position and type, so an asynchronous spawn can put the opening prompt on
// screen before the worktree those files land in exists.
func spawnAttachmentRefs(attachments []ports.SpawnAttachment) []string {
	refs := make([]string, 0, len(attachments))
	for i, a := range attachments {
		refs = append(refs, attachmentsDir+"/"+spawnAttachmentName(i, a))
	}
	return refs
}

func (m *Manager) importAttachments(ctx context.Context, rec domain.SessionRecord) error {
	if rec.Metadata.WorkspacePath == "" {
		return nil
	}
	return m.attachments.ImportWorkspace(ctx, rec.ID, rec.Metadata.WorkspacePath)
}

func (m *Manager) restoreAttachments(ctx context.Context, id domain.SessionID, workspace ports.WorkspaceInfo) error {
	_, err := m.attachments.MaterializeWorkspace(ctx, id, workspace.Path, func() error {
		if err := m.workspace.AddExclude(ctx, workspace, "/"+attachmentsDir+"/"); err != nil {
			return fmt.Errorf("exclude restored attachments: %w", err)
		}
		return nil
	})
	return err
}

func (m *Manager) cleanupAttachments(ctx context.Context, id domain.SessionID) {
	if err := m.attachments.RemoveSession(ctx, id); err != nil {
		m.logger.Warn("attachment cleanup failed", "sessionID", id, "error", err)
	}
}

// appendAttachmentReferences appends a block listing the attached file paths so
// the agent knows to read them. Placed after the human's brief.
func appendAttachmentReferences(prompt string, refs []string) string {
	if len(refs) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString(prompt)
	if strings.TrimSpace(prompt) != "" {
		b.WriteString("\n\n")
	}
	b.WriteString("Attached files (read these files in the workspace for context):")
	for _, ref := range refs {
		b.WriteString("\n- ")
		b.WriteString(ref)
	}
	return b.String()
}
