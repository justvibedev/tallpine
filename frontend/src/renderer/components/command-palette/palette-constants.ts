/** Timing constants and the view stack type for the command palette. */
import { type WorkspaceFileSearchTarget } from "../../lib/command-palette";
import { type PRReviewState } from "../../lib/session-reviews";

export const PALETTE_REVIEW_STALE_TIME_MS = 60_000;
export const PALETTE_REVIEW_DEFER_MS = 120;
export const PALETTE_FILE_SEARCH_DEBOUNCE_MS = 200;
export const PALETTE_FILE_SEARCH_MIN_CHARS = 2;
// A search pages in at most this many archive pages (ARCHIVE_PAGE_SIZE sessions each).
export const PALETTE_ARCHIVE_MAX_PAGES = 6;
export const EMPTY_REVIEW_STATES: Readonly<Record<string, PRReviewState[]>> = {};
export type PaletteView =
	| { mode: "root" }
	| { mode: "session-actions"; sessionId: string }
	| { mode: "new-task"; projectId: string }
	| { mode: "file-session-picker"; projectId: string }
	| { mode: "file-search"; target: WorkspaceFileSearchTarget; parent: "root" | "picker" };

// Memoized (it takes no props) so the shell's re-render on every workspace write
// does not re-render, and rebuild every command of, a palette that is closed.
