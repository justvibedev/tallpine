import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useLocation, useNavigate, useParams } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCommandPaletteEnabled } from "../../hooks/useCommandPaletteEnabled";
import { useRestoreSession } from "../../hooks/useRestoreSession";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { ARCHIVE_PAGE_SIZE, useArchivedSessions } from "../../hooks/useArchivedSessions";
import { buildCommands, backlogAddQueryItem, buildFileSessionCommands, buildSessionActions, buildWorkspaceFileCommands, displayGroups, filterCommands, findSession, isBacklogPanelAvailable, sessionSupportsFileSearch, workspaceFileSearchTarget } from "../../lib/command-palette";
import { type CommandPaletteFileSearchResponse, commandPaletteFileSearchQueryOptions } from "../../lib/command-palette-file-search";
import { sessionReviewsQueryOptions, type PRReviewState } from "../../lib/session-reviews";
import { useShell } from "../../lib/shell-context";
import { openPRs, workerSessions } from "../../types/workspace";
import { useUiStore } from "../../stores/ui-store";
import { PaletteView, PALETTE_ARCHIVE_MAX_PAGES, PALETTE_FILE_SEARCH_DEBOUNCE_MS, PALETTE_FILE_SEARCH_MIN_CHARS, PALETTE_REVIEW_DEFER_MS, PALETTE_REVIEW_STALE_TIME_MS, EMPTY_REVIEW_STATES } from "./palette-constants";

/** Palette state, queries and derived command items. */
export function usePaletteData() {
	const { i18n, t } = useTranslation();
	const enabled = useCommandPaletteEnabled();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const restoreSessionById = useRestoreSession();
	const { pathname } = useLocation();
	const params = useParams({ strict: false }) as { hostId?: string; projectId?: string; sessionId?: string };
	const { cloneProject, createProject, initializeProjectRepository } = useShell();
	const resolvedTheme = useUiStore((s) => s.resolvedTheme);
	const setThemePreference = useUiStore((s) => s.setThemePreference);
	const isOpen = useUiStore((s) => s.isCommandPaletteOpen);
	const setOpen = useUiStore((s) => s.setCommandPaletteOpen);
	const restartingProjectIds = useUiStore((s) => s.restartingProjectIds);
	// The palette stays mounted to preserve its close animation and global
	// shortcut. While closed, commands are invisible, so retain the cached
	// snapshot without subscribing this hidden surface to streamed updates.
	const liveWorkspaces = useWorkspaceQuery({ subscribed: isOpen }).data ?? [];

	const [view, setView] = useState<PaletteView>({ mode: "root" });
	const [query, setQuery] = useState("");
	const [debouncedQuery, setDebouncedQuery] = useState("");
	// The live list holds no terminated sessions; a search reaches them by paging
	// the archive in, a bounded number of pages, only while there is text to match.
	const searchingArchive = isOpen && view.mode === "root" && debouncedQuery.trim().length >= 2;
	const archive = useArchivedSessions({}, { enabled: searchingArchive });
	const archivePagesLoaded = Math.ceil(archive.sessions.length / ARCHIVE_PAGE_SIZE);
	useEffect(() => {
		if (searchingArchive && archive.hasNextPage && !archive.isFetching && archivePagesLoaded < PALETTE_ARCHIVE_MAX_PAGES) void archive.fetchNextPage();
	}, [archive, archivePagesLoaded, searchingArchive]);
	const workspaces = useMemo(() => {
		if (!searchingArchive || archive.sessions.length === 0) return liveWorkspaces;
		return liveWorkspaces.map((workspace) => {
			const known = new Set(workspace.sessions.map((session) => session.id));
			const archived = archive.sessions.filter((session) => session.workspaceId === workspace.id && !known.has(session.id));
			return archived.length === 0 ? workspace : { ...workspace, sessions: [...workspace.sessions, ...archived] };
		});
	}, [archive.sessions, liveWorkspaces, searchingArchive]);
	const [selectedValue, setSelectedValue] = useState("");
	const [error, setError] = useState<string | null>(null);
	const [pendingId, setPendingId] = useState<string | null>(null);
	const [reviewStatesSnapshot, setReviewStatesSnapshot] = useState<Readonly<Record<string, PRReviewState[]>>>();
	const [reviewActionsReady, setReviewActionsReady] = useState(false);
	const [createProjectFlowMounted, setCreateProjectFlowMounted] = useState(false);
	const [createProjectFlowOpenSignal, setCreateProjectFlowOpenSignal] = useState(0);
	const [createProjectFlowPendingOpen, setCreateProjectFlowPendingOpen] = useState(false);
	const [pendingDismiss, setPendingDismiss] = useState<null | "pop" | "close">(null);
	const pendingRef = useRef(false);
	const composerWrapRef = useRef<HTMLDivElement>(null);
	const keepEditingRef = useRef<HTMLButtonElement>(null);
	const hadPendingDismissRef = useRef(false);
	useEffect(() => {
		const had = hadPendingDismissRef.current;
		hadPendingDismissRef.current = pendingDismiss !== null;
		if (pendingDismiss !== null) keepEditingRef.current?.focus();
		else if (had) composerWrapRef.current?.querySelector<HTMLElement>("input, textarea, [contenteditable='true']")?.focus();
	}, [pendingDismiss]);
	const runGenerationRef = useRef(0);
	const composerDirtyRef = useRef(false);
	const composerBusyRef = useRef(false);
	const viewRef = useRef(view);
	viewRef.current = view;
	const closeResetTimerRef = useRef<number | null>(null);

	const currentSessionContext = useMemo(() => {
		if (!params.sessionId) return undefined;
		for (const workspace of workspaces) {
			if ((workspace.hostId ?? "") !== (params.hostId ?? "")) continue;
			const session = workspace.sessions.find((candidate) => candidate.id === params.sessionId);
			if (session) return { workspace, session };
		}
		return undefined;
	}, [params.hostId, params.sessionId, workspaces]);
	const currentSession = currentSessionContext?.session;
	const currentProjectId = currentSession?.workspaceId ?? params.projectId;
	const currentWorkspace = useMemo(
		() => currentSessionContext?.workspace ?? workspaces.find((workspace) =>
			workspace.id === currentProjectId && (workspace.hostId ?? "") === (params.hostId ?? "")),
		[currentProjectId, currentSessionContext, params.hostId, workspaces],
	);
	const currentFileTarget = currentWorkspace && currentSession && sessionSupportsFileSearch(currentSession)
		? workspaceFileSearchTarget(currentWorkspace, currentSession)
		: undefined;

	useEffect(() => {
		if (!isOpen || view.mode === "new-task") {
			setDebouncedQuery("");
			return;
		}
		const timer = window.setTimeout(() => setDebouncedQuery(query.trim()), PALETTE_FILE_SEARCH_DEBOUNCE_MS);
		return () => window.clearTimeout(timer);
	}, [isOpen, query, view.mode]);

	const activeFileTarget = view.mode === "file-search"
		? view.target
		: view.mode === "root"
			? currentFileTarget
			: undefined;
	const normalizedFileQuery = debouncedQuery.trim();
	const fileSearchEnabled = isOpen
		&& normalizedFileQuery.length >= PALETTE_FILE_SEARCH_MIN_CHARS
		&& activeFileTarget !== undefined;
	const fileSearchOptions = commandPaletteFileSearchQueryOptions({
		target: activeFileTarget ?? { projectId: "", sessionId: "" },
		query: normalizedFileQuery,
		errorMessage: t("command.searchFilesError"),
	});
	const fileSearchQuery = useQuery<CommandPaletteFileSearchResponse>({ ...fileSearchOptions, enabled: fileSearchEnabled });

	const sessionsWithOpenPRs = useMemo(
		() =>
			(reviewActionsReady ? workspaces : []).flatMap((workspace) =>
				workerSessions(workspace.sessions).filter((session) => openPRs(session).length > 0),
			),
		[reviewActionsReady, workspaces],
	);
	// Opening the palette must first acknowledge the shortcut and focus its input.
	// Review actions are secondary and may require one query per PR-bearing session,
	// so defer their setup until the palette has had a chance to paint.
	useEffect(() => {
		if (!isOpen) {
			setReviewActionsReady(false);
			return;
		}
		const timer = window.setTimeout(() => setReviewActionsReady(true), PALETTE_REVIEW_DEFER_MS);
		return () => window.clearTimeout(timer);
	}, [isOpen]);
	// Review states are fetched only while the palette is open; the shared query
	// key means sessions already viewed in the inspector reuse the cached data.
	const reviewQuerySummary = useQueries({
		subscribed: reviewActionsReady,
		queries: sessionsWithOpenPRs.map((session) =>
			sessionReviewsQueryOptions(session, reviewActionsReady, PALETTE_REVIEW_STALE_TIME_MS),
		),
		combine: (results) => {
			const reviewStatesBySessionId: Record<string, PRReviewState[]> = {};
			results.forEach((result, index) => {
				const session = sessionsWithOpenPRs[index];
				if (session && result.data && !result.isFetching && !result.isError) {
					reviewStatesBySessionId[session.id] = result.data.reviews ?? [];
				}
			});
			return { reviewStatesBySessionId };
		},
	});

	// Each session's review action stays hidden until we know whether it's
	// safe to trigger, then is frozen for the rest of this open — merging in
	// newly-resolved sessions but never overwriting one already captured, so
	// a later poll (e.g. a running review completing) can't retitle or
	// re-sort a row the user may already have selected.
	useEffect(() => {
		if (!isOpen) {
			setReviewStatesSnapshot({});
			return;
		}
		if (!reviewActionsReady) return;
		setReviewStatesSnapshot((previous) => {
			let changed = false;
			const next = { ...previous };
			for (const [sessionId, reviews] of Object.entries(reviewQuerySummary.reviewStatesBySessionId)) {
				if (!(sessionId in next)) {
					next[sessionId] = reviews;
					changed = true;
				}
			}
			return changed ? next : previous;
		});
	}, [isOpen, reviewActionsReady, reviewQuerySummary.reviewStatesBySessionId]);
	const reviewStatesForCommands = isOpen && reviewActionsReady ? reviewStatesSnapshot : EMPTY_REVIEW_STATES;

	const rootItems = useMemo(
		() =>
			buildCommands({
				workspaces,
				currentProjectId,
				currentSessionId: params.sessionId,
				currentHostId: params.hostId,
				restartingProjectIds,
				reviewStatesBySessionId: reviewStatesForCommands,
				backlogPanelAvailable: isBacklogPanelAvailable({ hostId: params.hostId, sessionId: params.sessionId, pathname }),
			}, t),
		[workspaces, currentProjectId, params.hostId, params.sessionId, pathname, restartingProjectIds, reviewStatesForCommands, t, i18n.resolvedLanguage],
	);
	const scoped = useMemo(
		() => (view.mode === "session-actions" ? findSession(workspaces, view.sessionId) : undefined),
		[view, workspaces],
	);
	const sessionActionItems = useMemo(
		() => (scoped ? buildSessionActions(scoped.workspace, scoped.session, t) : []),
		[scoped, t],
	);
	const filePickerWorkspace = view.mode === "file-session-picker"
		? workspaces.find((workspace) => workspace.id === view.projectId && (workspace.hostId ?? "") === (params.hostId ?? ""))
		: undefined;
	const fileSessionItems = useMemo(
		() => filePickerWorkspace ? buildFileSessionCommands(filePickerWorkspace) : [],
		[filePickerWorkspace],
	);
	const fileItems = useMemo(
		() => activeFileTarget && fileSearchQuery.data
			? buildWorkspaceFileCommands(activeFileTarget, fileSearchQuery.data.results)
			: [],
		[activeFileTarget, fileSearchQuery.data],
	);

	const groups = useMemo(() => {
		if (view.mode === "session-actions") {
			return [{ id: "actions", label: "", items: filterCommands(sessionActionItems, query) }];
		}
		if (view.mode === "file-session-picker") {
			return [{ id: "file-sessions", label: "", items: filterCommands(fileSessionItems, query) }];
		}
		if (view.mode === "file-search") {
			return displayGroups(fileItems, normalizedFileQuery, t);
		}
		const settledFileItems = normalizedFileQuery === query.trim() ? fileItems : [];
		const ranked = displayGroups([...rootItems, ...settledFileItems], query, t);
		// Add "<query>" to backlog is always the last row, outside the ranked groups.
		const addToBacklog = backlogAddQueryItem(query, params.hostId ? undefined : currentProjectId, t);
		return addToBacklog ? [...ranked, { id: "backlog-add-query", label: "", items: [addToBacklog] }] : ranked;
	}, [view.mode, rootItems, sessionActionItems, fileSessionItems, fileItems, query, normalizedFileQuery, t, i18n.resolvedLanguage, params.hostId, currentProjectId]);

	const visibleItems = useMemo(() => groups.flatMap((group) => group.items), [groups]);
	const value =
		(visibleItems.some((item) => item.id === selectedValue && !item.disabled)
			? selectedValue
			: (visibleItems.find((item) => !item.disabled) ?? visibleItems[0])?.id) ?? "";

	return {
		activeFileTarget,
		cloneProject,
		closeResetTimerRef,
		composerBusyRef,
		composerDirtyRef,
		composerWrapRef,
		createProject,
		createProjectFlowMounted,
		createProjectFlowOpenSignal,
		createProjectFlowPendingOpen,
		currentFileTarget,
		enabled,
		error,
		fileItems,
		fileSearchQuery,
		groups,
		initializeProjectRepository,
		isOpen,
		keepEditingRef,
		navigate,
		normalizedFileQuery,
		params,
		pendingDismiss,
		pendingId,
		pendingRef,
		query,
		queryClient,
		resolvedTheme,
		restoreSessionById,
		runGenerationRef,
		scoped,
		setCreateProjectFlowMounted,
		setCreateProjectFlowOpenSignal,
		setCreateProjectFlowPendingOpen,
		setDebouncedQuery,
		setError,
		setOpen,
		setPendingDismiss,
		setPendingId,
		setQuery,
		setSelectedValue,
		setThemePreference,
		setView,
		t,
		value,
		view,
		viewRef,
		workspaces,
	};
}
