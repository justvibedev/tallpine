/** Permission row presentation. */
import { Check, Hand, PencilLine, ShieldAlert, ShieldCheck, type LucideIcon } from "lucide-react";
import { OptionMenuItem } from "../../ui/option-menu";
import { cn } from "../../../lib/utils";
import type { ApprovalMode } from "../../../types/conversation";

/** Codex names its modes differently: its default has a free hand and its accept-edits asks first. */
export function permissionLook(harness: string | undefined, mode: ApprovalMode): ApprovalMode {
	if (harness !== "codex") return mode;
	return mode === "default" ? "bypass-permissions" : mode === "accept-edits" ? "default" : mode;
}

export function permissionIcon(mode: ApprovalMode | undefined): LucideIcon {
	if (mode === "bypass-permissions") return ShieldAlert;
	if (mode === "accept-edits") return PencilLine;
	if (mode === "auto") return ShieldCheck;
	return Hand;
}

/** One permission mode: what it is called, what it lets the agent do, and whether it is on. */
export function PermissionRow({
	mode,
	title,
	description,
	active,
	disabled,
	onSelect,
}: {
	mode?: ApprovalMode;
	title: string;
	description?: string;
	active: boolean;
	/** The agent picked for the next message cannot run in this mode. */
	disabled?: boolean;
	onSelect: () => void;
}) {
	const Icon = permissionIcon(mode);
	const unrestricted = mode === "bypass-permissions";
	return (
		<OptionMenuItem active={active} radio disabled={disabled} onSelect={onSelect} className="items-start gap-3 whitespace-normal px-3 py-2">
			<Icon aria-hidden="true" className={cn("mt-0.5 size-4 shrink-0", unrestricted ? "text-warning" : "text-muted-foreground")} />
			<span className="flex min-w-0 flex-1 flex-col gap-0.5">
				<span className={cn("text-xs", unrestricted ? "text-warning" : "text-foreground")}>{title}</span>
				{description ? (
					<span className={cn("truncate text-[11px] leading-snug", unrestricted ? "text-warning/80" : "text-muted-foreground")}>
						{description}
					</span>
				) : null}
			</span>
			{active ? <Check aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-foreground" /> : null}
		</OptionMenuItem>
	);
}
