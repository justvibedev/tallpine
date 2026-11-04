// Package migratevo implements `migrate-from-vo`, the one-shot migrator that
// moves a Vibe Orchestrator install (~/.vo) to its Tallpine home (~/.tallpine).
//
// It is dormant until the brand flip (nothing calls it yet). Every path is
// derived from Roots, so tests and rehearsals inject a scratch home; no code
// here touches the real home unless Roots says so.
//
// The procedure is a resumable list of idempotent steps (see steps.go). The
// plan and per-step done flags live in a plan file next to the home dir, so a
// crash at any point is resumed by re-running the command.
package migratevo
