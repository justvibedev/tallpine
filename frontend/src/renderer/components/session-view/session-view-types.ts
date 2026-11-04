/** Types shared by SessionView and its hooks. */
import type { components } from "../../../api/schema";
import type { FileOpenOptions, FileViewMode } from "../FileContentPane";

export type CenterFileOpenRequest = { path?: string; source?: FileOpenOptions["source"]; previousPath?: string; commitSha?: string; editing: boolean; key: number; line?: number; mode: FileViewMode; scope?: FileOpenOptions["scope"] };

export type ReviewsResponse = components["schemas"]["ListReviewsResponse"];
export type ReviewerSurface = components["schemas"]["DomainReviewerSurface"];
export type ReviewerTerminalTarget = { handleId: string; harness: string };
export type ReviewerChatTarget = { reviewId: string; harness: string };

export type BrowserPopOutPhase = "docked" | "mounting" | "open";
export type BrowserPopOutState = {
	sessionId: string;
	phase: BrowserPopOutPhase;
};


export type SessionViewProps = {
	sessionId: string;
	projectId?: string;
	hostId?: string;
};
import type { useSessionViewData } from "./useSessionViewData";
import type { useSessionViewTerminals } from "./useSessionViewTerminals";
import type { useSessionViewWorkspace } from "./useSessionViewWorkspace";
import type { useSessionViewActions } from "./useSessionViewActions";
import type { useSessionViewChrome } from "./useSessionViewChrome";

/** Merged hook state SessionView hands to its render sections. */
export type SessionViewState = ReturnType<typeof useSessionViewData> &
	ReturnType<typeof useSessionViewTerminals> &
	ReturnType<typeof useSessionViewWorkspace> &
	ReturnType<typeof useSessionViewActions> &
	ReturnType<typeof useSessionViewChrome>;
