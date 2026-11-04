/** Side-by-side split diff view. */
import { useMemo } from "react";
import type { TFunction } from "i18next";
import { cn } from "../../lib/utils";
import type { DiffRow } from "../../lib/diff-parser";
import type { DiffRun } from "../../lib/diff-highlight";
import { FileAnnotationModel, fileAnnotationKey } from "./diff-annotation-types";
import { toSplitRows, HunkBand } from "./DiffRows";
import { rowAnnotationTargets, lineAnnotationTarget, LineFeedbackButton } from "./LineFeedback";
import { FileAnnotationComposer } from "./FileAnnotationComposer";
import { diffRowTone } from "./DiffView";
import { renderDiffRuns } from "./DiffChrome";

export function SplitDiff({
	annotation,
	newRuns,
	oldRuns,
	path,
	previousPath,
	rows,
	t,
}: {
	annotation: FileAnnotationModel;
	newRuns: DiffRun[][];
	oldRuns: DiffRun[][];
	path: string;
	previousPath?: string;
	rows: DiffRow[];
	t: TFunction;
}) {
	const splitRows = useMemo(() => toSplitRows(rows), [rows]);
	return (
		<div>
			{splitRows.map((splitRow, index) =>
				splitRow.kind === "hunk" ? (
					<HunkBand key={`sh${index}`} row={splitRow.row} />
				) : (
					<div key={`sp${index}`}>
						<div className="grid grid-cols-2 divide-x divide-border/40">
							<SplitSide
								annotation={annotation}
								path={path}
								previousPath={previousPath}
								row={splitRow.left}
								rowIndex={splitRow.leftIndex}
								runs={splitRow.leftIndex === null ? null : oldRuns[splitRow.leftIndex]}
								side="old"
								t={t}
							/>
							<SplitSide
								annotation={annotation}
								path={path}
								previousPath={previousPath}
								row={splitRow.right}
								rowIndex={splitRow.rightIndex}
								runs={splitRow.rightIndex === null ? null : newRuns[splitRow.rightIndex]}
								side="new"
								t={t}
							/>
						</div>
						{[splitRow.leftIndex, splitRow.leftIndex === splitRow.rightIndex ? null : splitRow.rightIndex].flatMap((rowIndex) =>
							rowIndex === null ? [] : rowAnnotationTargets(annotation.targets, path, rowIndex),
						).map((target) => (
							<FileAnnotationComposer annotation={annotation} key={fileAnnotationKey(target)} target={target} />
						))}
					</div>
				),
			)}
		</div>
	);
}

export function SplitSide({
	annotation,
	path,
	previousPath,
	row,
	rowIndex,
	runs,
	side,
	t,
}: {
	annotation: FileAnnotationModel;
	path: string;
	previousPath?: string;
	row: DiffRow | null;
	rowIndex: number | null;
	runs: DiffRun[] | null;
	side: "old" | "new";
	t: TFunction;
}) {
	if (!row || rowIndex === null || !runs) return <div className="bg-surface-faint/20" aria-hidden="true" />;
	const lineNo = side === "old" ? row.oldNo : row.newNo;
	const tone = row.kind === "hunk" ? "" : diffRowTone[row.kind];
	const target = lineNo == null ? null : lineAnnotationTarget(path, previousPath, row, rowIndex, side);
	return (
		<div
			className={cn("group/line relative flex min-w-0", tone)}
			data-diff-row=""
			data-kind={row.kind}
			data-new-no={row.newNo ?? ""}
			data-old-no={row.oldNo ?? ""}
			data-row-index={rowIndex}
		>
			{target ? (
				<LineFeedbackButton
					active={rowAnnotationTargets(annotation.targets, path, rowIndex).some((open) => open.side === side)}
					onClick={() => annotation.begin(target)}
					t={t}
					target={target}
				/>
			) : null}
			<span className="w-9 shrink-0 select-none border-r border-border/50 bg-terminal px-1.5 text-right text-passive/70 tabular-nums">
				{lineNo ?? ""}
			</span>
			<span className="min-w-0 whitespace-pre-wrap break-all px-1.5">{renderDiffRuns(runs, row.kind === "add")}</span>
		</div>
	);
}
