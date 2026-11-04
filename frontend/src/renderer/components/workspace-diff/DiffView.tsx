/** Unified diff view with row virtualization. */
import "../chat/code-theme.css";
import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { useTranslation } from "react-i18next";
import { useDiffHighlight } from "../../hooks/useDiffHighlight";
import { cn } from "../../lib/utils";
import type { DiffRow, DiffRowKind } from "../../lib/diff-parser";
import { FileAnnotationModel } from "./diff-annotation-types";
import { SplitDiff } from "./SplitDiff";
import { DiffRowContent } from "./DiffRows";

export const diffRowTone: Record<Exclude<DiffRowKind, "hunk">, string> = {
	add: "bg-success/10",
	del: "bg-error/10",
	context: "",
};

export const diffMarkerGlyph: Record<Exclude<DiffRowKind, "hunk">, string> = {
	add: "+",
	del: "-",
	context: " ",
};

// A real (non-collapsed) selection whose
// anchor lives inside this DiffView's scroll container.
export function isSelectionActiveIn(selection: Selection | null, container: HTMLElement | null): boolean {
	if (!selection || !container || selection.isCollapsed) return false;
	return selection.anchorNode !== null && container.contains(selection.anchorNode);
}

// SessionFilesView has no per-file scroll box — the Files panel is one
// continuous list, and an expanded file's rows scroll as part of that same
// shared container (`[data-files-scroll-root]`). Below the threshold every
// row just renders directly, unchanged from before (the vast majority of
// diffs never pay for any of this). Above it, only the rows actually
// scrolled into view are mounted, via @tanstack/react-virtual attached to
// that SHARED scroll container rather than a container of this file's own —
// `scrollMargin` is this file's row-list's offset from the top of the shared
// scrollable content, since the virtualizer needs that to know which of its
// rows fall inside the current viewport. That offset shifts whenever
// anything above this file changes height (another file expanding or
// collapsing, or gaining/losing its own virtualized rows), so it's
// re-measured via a ResizeObserver on the full file list rather than once.
// Rows wrap (`whitespace-pre-wrap`) and the panel's available width varies a
// lot — full window width when the Files view is maximized, much narrower
// when docked alongside the terminal — so a long line can be one row of
// height in the maximized view and several wrapped rows tall when docked.
// ESTIMATED_ROW_HEIGHT is only the initial guess for a row that hasn't
// rendered yet; `measureElement` corrects it to the row's real height once
// mounted, which is what keeps rows from overlapping when they wrap.
export const ROW_VIRTUALIZE_THRESHOLD = 150;
export const ESTIMATED_ROW_HEIGHT = 20;

export function useSharedScrollRowVirtualizer(
	scrollContainerRef: RefObject<HTMLElement | null>,
	count: number,
	enabled: boolean,
) {
	const listRef = useRef<HTMLDivElement>(null);
	const [scrollElement, setScrollElement] = useState<HTMLElement | null>(null);
	const [scrollMargin, setScrollMargin] = useState(0);

	useEffect(() => {
		if (!enabled) return;
		setScrollElement(scrollContainerRef.current?.closest<HTMLElement>("[data-files-scroll-root]") ?? null);
	}, [enabled, scrollContainerRef]);

	// A regular effect, not useLayoutEffect: "Expand All" mounts many DiffView
	// instances in the same commit, and React runs every layout effect from
	// that commit synchronously, before the browser is allowed to paint
	// anything. A read (getBoundingClientRect) here forces a synchronous
	// layout — N of those back to back, all before first paint, is exactly
	// what made "Expand All" stall. A plain effect still measures almost
	// immediately (one frame later at worst), which is an imperceptible
	// one-time cost for something that self-corrects, in exchange for never
	// blocking paint on how many files just got expanded at once.
	useEffect(() => {
		if (!enabled || !scrollElement || !listRef.current) return;
		const measure = () => {
			if (!listRef.current) return;
			const listRect = listRef.current.getBoundingClientRect();
			const scrollRect = scrollElement.getBoundingClientRect();
			setScrollMargin(listRect.top - scrollRect.top + scrollElement.scrollTop);
		};
		measure();
		const resizeTarget = scrollElement.querySelector<HTMLElement>(".session-files-review-list") ?? scrollElement;
		const observer = new ResizeObserver(measure);
		observer.observe(resizeTarget);
		return () => observer.disconnect();
	}, [enabled, scrollElement]);

	const virtualizer = useVirtualizer({
		count,
		getScrollElement: () => (enabled ? scrollElement : null),
		estimateSize: () => ESTIMATED_ROW_HEIGHT,
		// Every row outside the viewport that overscan pulls in is a row that
		// still has to pay full first-time cost (render + a real DOM
		// measurement, see useSharedScrollRowVirtualizer's comment above) the
		// first time it's scrolled into range. A smaller buffer means fewer
		// never-before-seen rows get force-measured on each scroll jump,
		// trading a slightly higher chance of a one-frame blank edge during
		// very fast scrolling for less of that first-pass cost on big files.
		overscan: 8,
		scrollMargin,
	});

	return { listRef, virtualizer };
}

export function DiffView({
	annotation,
	onActiveSelectionChange,
	path,
	previousPath,
	rows,
	split,
	truncated,
	wrap,
}: {
	annotation: FileAnnotationModel;
	onActiveSelectionChange: (active: boolean) => void;
	path: string;
	previousPath?: string;
	rows: DiffRow[];
	split: boolean;
	truncated?: boolean;
	wrap: boolean;
}) {
	const { t } = useTranslation();
	const containerRef = useRef<HTMLDivElement>(null);
	const [hasSelection, setHasSelection] = useState(false);
	const shouldVirtualize = !split && rows.length > ROW_VIRTUALIZE_THRESHOLD;
	const { listRef, virtualizer } = useSharedScrollRowVirtualizer(containerRef, rows.length, shouldVirtualize);
	const highlight = useDiffHighlight(rows, path, previousPath);
	const runs = useMemo(
		() => rows.map((row, index) => (row.kind === "del" ? highlight.oldSide[index] : highlight.newSide[index])),
		[rows, highlight],
	);

	useEffect(() => {
		const onSelectionChange = () => {
			setHasSelection(isSelectionActiveIn(window.getSelection(), containerRef.current));
		};
		document.addEventListener("selectionchange", onSelectionChange);
		return () => document.removeEventListener("selectionchange", onSelectionChange);
	}, []);

	useEffect(() => {
		onActiveSelectionChange(hasSelection);
	}, [hasSelection, onActiveSelectionChange]);

	return (
		<div>
			{truncated ? (
				<div className="shrink-0 border-b border-border bg-warning/10 px-3 py-1.5 text-xs text-warning">
					{t("files.diffTruncated")}
				</div>
			) : null}
			<div
				className="diff-code session-files-diff-scrollbar select-text overflow-x-auto overflow-y-visible bg-terminal font-mono text-xs leading-row text-terminal-foreground"
				ref={containerRef}
			>
				{split ? (
					<SplitDiff
						annotation={annotation}
						newRuns={highlight.newSide}
						oldRuns={highlight.oldSide}
						path={path}
						previousPath={previousPath}
						rows={rows}
						t={t}
					/>
				) : shouldVirtualize ? (
					<div
						className={cn("relative", !wrap && "min-w-max")}
						ref={listRef}
						style={{ height: virtualizer.getTotalSize() }}
					>
						{virtualizer.getVirtualItems().map((virtualRow) => {
							const row = rows[virtualRow.index];
							return (
								<div
									data-index={virtualRow.index}
									key={virtualRow.index}
									ref={virtualizer.measureElement}
									style={{
										position: "absolute",
										top: 0,
										left: 0,
										width: "100%",
										transform: `translateY(${virtualRow.start - virtualizer.options.scrollMargin}px)`,
									}}
								>
									<DiffRowContent
										annotation={annotation}
										index={virtualRow.index}
										path={path}
										previousPath={previousPath}
										row={row}
										runs={runs[virtualRow.index]}
										t={t}
										wrap={wrap}
									/>
								</div>
							);
						})}
					</div>
				) : (
					<div className={cn(!wrap && "min-w-max")}>
						{rows.map((row, index) => (
							<DiffRowContent
								annotation={annotation}
								index={index}
								key={index}
								path={path}
								previousPath={previousPath}
								row={row}
								runs={runs[index]}
								t={t}
								wrap={wrap}
							/>
						))}
					</div>
				)}
			</div>
		</div>
	);
}
