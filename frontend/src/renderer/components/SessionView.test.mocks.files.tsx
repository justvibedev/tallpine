import { useEffect } from "react";
import { createPortal } from "react-dom";
import { vi } from "vitest";
import { useFilesTopbarHost } from "./files-topbar-host";
import type { BrowserAgentActivityState } from "../../main/browser-view-host";


const artifactFeedbackConsumes = vi.hoisted(() => [] as number[]);

const { browserDestroy, browserViewOptions, browserViewState } = vi.hoisted(() => ({
	browserDestroy: vi.fn(),
	browserViewOptions: { current: undefined as { active: boolean; sessionId: string; terminated: boolean } | undefined },
	browserViewState: { url: "", tabCount: 1, agentBrowserActive: false, agentBrowserActivity: null as BrowserAgentActivityState | null },
}));

vi.mock("./BrowserPanel", () => ({
	BrowserPanelView: ({
		poppedOut,
		onTogglePopOut,
	}: {
		poppedOut: boolean;
		onTogglePopOut: (next: boolean) => void;
	}) => (
		<button type="button" onClick={() => onTogglePopOut(!poppedOut)}>
			{poppedOut ? "browser center" : "browser rail"}
		</button>
	),
	useBrowserAnnotationQueue: () => ({
		status: "idle",
		error: "",
		queuedCount: 0,
		beginPicking: vi.fn(),
		cancelPicking: vi.fn(),
		enqueue: vi.fn(),
		failPicking: vi.fn(),
		retryQueued: vi.fn(),
	}),
}));

vi.mock("./SessionFileExplorer", () => ({
	SessionFileExplorer: ({
		isMaximized,
		onOpenFile,
		onSplitChange,
		onToggleMaximized,
		split,
	}: {
		isMaximized?: boolean;
		onOpenFile?: (path: string, options?: import("./FileContentPane").FileOpenOptions) => void;
		onSplitChange?: (split: boolean) => void;
		onToggleMaximized?: (next: boolean) => void;
		split?: boolean;
	}) => {
		const topbarHost = useFilesTopbarHost();
		return <div>
			{topbarHost ? createPortal(<input aria-label="files filter" />, topbarHost) : null}
			<button type="button" onClick={() => onToggleMaximized?.(!isMaximized)}>
				{isMaximized ? "files center" : "files rail"}
			</button>
			<button type="button" onClick={() => onSplitChange?.(!split)}>
				{split ? "use unified diff" : "use split diff"}
			</button>
			{!isMaximized && onOpenFile ? (
				<>
					<span>file tree</span>
 <button type="button" onClick={()=>onOpenFile("src/App.tsx",{mode:"file",source:{kind:"snapshot",view:{branch:"feature",expectedTipSha:"a".repeat(40)},label:"feature · aaaaaaa"}})}>open historical src/App.tsx</button>
					<button type="button" onClick={() => onOpenFile("src/App.tsx", { mode: "file" })}>
						select src/App.tsx
					</button>
					<button type="button" onClick={() => onOpenFile("src/App.tsx", { mode: "diff" })}>
						open diff src/App.tsx in center
					</button>
					<button type="button" onClick={() => onOpenFile("src/App.tsx", { editing: true, mode: "file" })}>
						edit src/App.tsx in center
					</button>
				</>
			) : null}
		</div>
	},
}));

vi.mock("./SessionFileWorkspace", () => ({
	SessionFileWorkspace: ({ annotation, initialEditing, initialLine, initialMode, initialRequestKey, onInitialLineConsumed, path, scope, split }: {
		annotation: {
			begin: (target: { path: string; scope: string; side: string; surface: string }) => void;
			draftFor: (target: { path: string }) => string;
			setDraft: (target: { path: string }, draft: string) => void;
			targets: Array<{ path: string }>;
		};
		initialEditing?: boolean;
		initialLine?: number;
		initialMode?: string;
		initialRequestKey?: number;
		onInitialLineConsumed?: (path: string, requestKey: number) => void;
		path: string;
		scope?: string;
		split: boolean;
	}) => {
		return (
			<div data-editing={String(Boolean(initialEditing))} data-line={initialLine} data-mode={initialMode} data-request-key={initialRequestKey} data-split={String(split)} data-testid="session-file-workspace">
				{path}
				{initialLine != null ? <button onClick={() => onInitialLineConsumed?.(path, initialRequestKey ?? 0)} type="button">consume initial line</button> : null}
				<button onClick={() => annotation.begin({ path, scope: scope ?? "combined", side: "file", surface: "focused" })} type="button">header feedback</button>
				{annotation.targets.map((target) => <input aria-label="feedback draft" key={target.path} onChange={(event) => annotation.setDraft(target, event.target.value)} value={annotation.draftFor(target)} />)}
			</div>
		);
	},
}));



vi.mock("../hooks/useBrowserView", () => ({
	useBrowserView: (options: { active: boolean; sessionId: string; terminated: boolean }) => {
		browserViewOptions.current = options;
		return {
			viewId: "browser:sess-1",
			navState: {
				viewId: "browser:sess-1",
				url: browserViewState.url,
				title: browserViewState.url ? "Calculator" : "",
				canGoBack: false,
				canGoForward: false,
				isLoading: false,
			},
			slotRef: vi.fn(),
			navigate: vi.fn(),
			goBack: vi.fn(),
			goForward: vi.fn(),
			reload: vi.fn(),
			stop: vi.fn(),
			tabs: browserViewState.tabCount ? [{ id: "t1", url: "http://127.0.0.1:4173/", title: "Calculator", active: true }] : [],
			activeTabId: browserViewState.tabCount ? "t1" : "",
			tabNotice: "",
			agentBrowserActive: browserViewState.agentBrowserActive,
			agentBrowserActivity: browserViewState.agentBrowserActivity,
			selectTab: vi.fn(),
			closeTab: vi.fn(),
			annotationMode: false,
			setAnnotationMode: vi.fn(),
			destroy: browserDestroy,
		};
	},
}));

export function ArtifactsViewMock({
	onRequestConsumed,
	request,
}: {
	onRequestConsumed?: (key: number) => void;
	request?: { feedback?: boolean; path: string; key: number } | null;
}) {
	useEffect(() => {
		if (!request?.feedback) return;
		artifactFeedbackConsumes.push(request.key);
		onRequestConsumed?.(request.key);
	}, [onRequestConsumed, request]);
	return <div>
		<span>artifacts view</span>
		{request ? <span>{`selected ${request.path}`}</span> : null}
		{request?.feedback ? <span>{`feedback request ${request.key}`}</span> : null}
	</div>;
}

export { artifactFeedbackConsumes, browserDestroy, browserViewOptions, browserViewState };
