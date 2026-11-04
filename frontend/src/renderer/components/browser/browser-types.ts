import type { useBrowserPanelState } from "./useBrowserPanelState";
import type { useBrowserPanelActions } from "./useBrowserPanelActions";
import type { useBrowserPanelChrome } from "./useBrowserPanelChrome";
/** Props and annotation queue model types for the browser panel. */
import { type BrowserViewModel } from "../../hooks/useBrowserView";
import { type BrowserAnnotationSubmitPayload } from "../../../shared/browser-annotations";
import type { WorkspaceSession } from "../../types/workspace";

export type BrowserPanelProps = {
	session: WorkspaceSession;
	active: boolean;
	poppedOut: boolean;
	onTogglePopOut: (next: boolean) => void;
	topbarHost?: HTMLElement | null;
};

export type AnnotationStatus = "idle" | "picking" | "queued" | "sending" | "sent" | "error";

export type BrowserAnnotationQueueModel = {
	status: AnnotationStatus;
	error: string;
	queuedCount: number;
	beginPicking: () => void;
	cancelPicking: () => void;
	enqueue: (payload: BrowserAnnotationSubmitPayload) => void;
	failPicking: (message: string) => void;
	retryQueued: () => void;
};

export type BrowserPanelViewProps = BrowserPanelProps & {
	annotationQueue: BrowserAnnotationQueueModel;
	browserView: BrowserViewModel;
};

export type BrowserPanelViewState = ReturnType<typeof useBrowserPanelState> &
	ReturnType<typeof useBrowserPanelActions> &
	ReturnType<typeof useBrowserPanelChrome>;
