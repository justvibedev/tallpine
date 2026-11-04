// What the next flush must refresh, filled by incoming events.
export type PendingState = {
	conversationSessions: Set<string>;
	reviewerConversations: Set<string>;
	reviewSessions: Set<string>;
	interfaceTransitionSessions: Set<string>;
	editorHandoffSessions: Set<string>;
	modelCatalogScopes: Map<string, { agentId: string; projectId: string }>;
	backlogProjects: Set<string>;
	allBacklogInvalidation: boolean;
	workspaceInvalidation: boolean;
	allConversationsInvalidation: boolean;
	allEditorHandoffsInvalidation: boolean;
	// Reviews, interface transitions and model catalogs are otherwise only
	// refreshed by their own events. A fresh stream starts at the newest event,
	// so a lifecycle refresh must cover whatever changed in the gap.
	allEventScopedCachesInvalidation: boolean;
	immediateWorkspaceRefetch: boolean;
	// Sessions waiting for the next flush to be re-read, and the id-only
	// updates waiting out their refresh interval before joining them.
	sessionPatches: Set<string>;
	fullWorkspaceRefetchRequested: boolean;
};

export function createPendingState(): PendingState {
	return {
		conversationSessions: new Set(),
		reviewerConversations: new Set(),
		reviewSessions: new Set(),
		interfaceTransitionSessions: new Set(),
		editorHandoffSessions: new Set(),
		modelCatalogScopes: new Map(),
		backlogProjects: new Set(),
		allBacklogInvalidation: false,
		workspaceInvalidation: false,
		allConversationsInvalidation: false,
		allEditorHandoffsInvalidation: false,
		allEventScopedCachesInvalidation: false,
		immediateWorkspaceRefetch: false,
		sessionPatches: new Set(),
		fullWorkspaceRefetchRequested: false,
	};
}
