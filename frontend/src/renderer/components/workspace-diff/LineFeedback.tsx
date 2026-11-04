/** Per-line feedback targets and button controls. */
import type { TFunction } from "i18next";
import { Plus } from "lucide-react";
import { cn } from "../../lib/utils";
import type { DiffRow } from "../../lib/diff-parser";
import { Button } from "../ui/button";
import { ActiveFileAnnotationTarget } from "./diff-annotation-types";

export function lineAnnotationTarget(
	path: string,
	previousPath: string | undefined,
	row: DiffRow,
	rowIndex: number,
	side: "old" | "new" = row.kind === "del" ? "old" : "new",
): ActiveFileAnnotationTarget {
	return {
		path,
		previousPath,
		side,
		line: side === "old" ? (row.oldNo ?? undefined) : (row.newNo ?? undefined),
		oldLine: row.oldNo ?? undefined,
		newLine: row.newNo ?? undefined,
		lineKind: row.kind === "hunk" ? undefined : row.kind,
		lineText: row.text,
		rowIndex,
		surface: "focused",
	};
}

export function rowAnnotationTargets(targets: ActiveFileAnnotationTarget[], path: string, rowIndex: number): ActiveFileAnnotationTarget[] {
	return targets.filter((target) => target.surface !== "review" && target.path === path && target.side !== "file" && target.rowIndex === rowIndex);
}

// `t` comes from the caller (which already holds one useTranslation()
// subscription) rather than calling useTranslation() here: this button is
// invisible until hover on every diff row, and a virtualized file can mount
// dozens of them per scroll tick — a separate i18n context subscription per
// row added up on large files.
export function LineFeedbackButton({
	active,
	onClick,
	t,
	target,
}: {
	active: boolean;
	onClick: () => void;
	t: TFunction;
	target: ActiveFileAnnotationTarget;
}) {
	const side = t(target.side === "old" ? "files.oldSide" : "files.newSide");
	const label = t("files.addLineFeedback", { file: target.path, line: target.line, side });
	return <LineFeedbackButtonControl expanded={active} label={label} onClick={onClick} />;
}

// The single Tallpine line-feedback affordance used by both the original diff rows
// and the Pierre-backed viewers. Pierre only provides the hovered-line slot;
// the control, styling, and annotation behavior remain Tallpine-owned.
export function LineFeedbackButtonControl({
	expanded = false,
	gutter = false,
	label,
	onClick,
}: {
	expanded?: boolean;
	gutter?: boolean;
	label: string;
	onClick: () => void;
}) {
	return (
		<Button
			aria-label={label}
			aria-expanded={expanded || undefined}
			className={cn(
				"z-20 size-6 rounded-sm border-primary/70 shadow-md shadow-black/30 active:translate-y-0 active:scale-100",
				gutter
					? "relative size-5"
					: "absolute inset-y-0 left-6 my-auto opacity-0 transition-opacity focus-visible:opacity-100 group-hover/line:opacity-100",
			)}
			data-utility-button={gutter ? "" : undefined}
			onClick={(event) => {
				event.stopPropagation();
				onClick();
			}}
			size={null}
			type="button"
			variant="primary"
		>
			<Plus className="size-4" aria-hidden="true" />
		</Button>
	);
}
