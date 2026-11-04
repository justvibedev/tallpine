import { useCallback, useEffect, useRef, useState } from "react";
import { expandedProjectsStorageKey, readExpandedProjectIds } from "./expanded-projects-storage";
import { type Selection } from "./types";

export function useProjectDisclosure({ selection, isCollapsed }: { selection: Selection; isCollapsed: boolean }) {
	const initialActiveSessionProjectId = useRef(
		selection.activeSessionId ? selection.activeProjectId : undefined,
	).current;

	// Disclosure state is persisted as the IDs of projects that were expanded.
	// An empty/missing store intentionally means all projects start collapsed.
	const [expandedIds, setExpandedIds] = useState<ReadonlySet<string>>(() => readExpandedProjectIds());
	const [collapsedRemoteProjects, setCollapsedRemoteProjects] = useState<ReadonlySet<string>>(() => new Set());
	const [dismissedInitialActiveProjectIds, setDismissedInitialActiveProjectIds] = useState<ReadonlySet<string>>(
		() => new Set(),
	);
	const toggleProjectDisclosure = useCallback((id: string) => {
		const routeFallbackActive =
			initialActiveSessionProjectId === id && !dismissedInitialActiveProjectIds.has(id);
		const currentlyExpanded = expandedIds.has(id) || routeFallbackActive;
		setExpandedIds((prev) => {
			const next = new Set(prev);
			currentlyExpanded ? next.delete(id) : next.add(id);
			if (typeof window !== "undefined") {
				window.localStorage?.setItem(expandedProjectsStorageKey, JSON.stringify([...next]));
			}
			return next;
		});
		setDismissedInitialActiveProjectIds((prev) => {
			if (initialActiveSessionProjectId !== id) return prev;
			const next = new Set(prev);
			currentlyExpanded ? next.add(id) : next.delete(id);
			return next;
		});
	}, [dismissedInitialActiveProjectIds, expandedIds, initialActiveSessionProjectId]);
	// Section disclosure: every section header collapses its own body.
	const [pinnedOpen, setPinnedOpen] = useState(true);
	const [projectsOpen, setProjectsOpen] = useState(true);
	const [scratchpadOpen, setScratchpadOpen] = useState(true);

	// Keyboard navigation (⌘⇧[ / ⌘⇧], ⌘1-9, attention jumps) can land on a row
	// that is scrolled out, in a collapsed project or inside a closed section.
	// Open whatever hides the active session, then scroll its row into view.
	// Only a change of session triggers this, so a project the user collapses
	// while staying on a session is left alone.
	const activeSessionKey = selection.activeSessionId ?? selection.activeRemoteSessionId;
	const activeProjectKey = selection.activeProjectId;
	useEffect(() => {
		if (!activeSessionKey || isCollapsed) return;
		if (activeProjectKey) {
			setExpandedIds((prev) => {
				if (prev.has(activeProjectKey)) return prev;
				const next = new Set(prev);
				next.add(activeProjectKey);
				window.localStorage?.setItem(expandedProjectsStorageKey, JSON.stringify([...next]));
				return next;
			});
		}
		let frame = 0;
		let attempts = 0;
		const reveal = () => {
			const row = document.querySelector<HTMLElement>(
				"[data-slot='sidebar-container'] [data-session-row] button[aria-current='page']",
			);
			if (row) {
				row.scrollIntoView?.({ block: "nearest" });
				return;
			}
			// Not rendered yet: its section may be closed. Open them all once, then retry.
			if (attempts === 0) {
				setPinnedOpen(true);
				setProjectsOpen(true);
				setScratchpadOpen(true);
			}
			if (attempts++ < 3) frame = requestAnimationFrame(reveal);
		};
		frame = requestAnimationFrame(() => requestAnimationFrame(reveal));
		return () => cancelAnimationFrame(frame);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [activeSessionKey]);

	return {
		initialActiveSessionProjectId,
		expandedIds,
		collapsedRemoteProjects,
		setCollapsedRemoteProjects,
		dismissedInitialActiveProjectIds,
		toggleProjectDisclosure,
		pinnedOpen,
		setPinnedOpen,
		projectsOpen,
		setProjectsOpen,
		scratchpadOpen,
		setScratchpadOpen,
	};
}
