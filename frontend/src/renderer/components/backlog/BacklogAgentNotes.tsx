import { ChevronRight } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { BacklogEditableText } from "./BacklogEditableText";

type BacklogAgentNotesProps = {
	notes: string;
	onSave: (notes: string) => Promise<unknown>;
};

/** The orchestrator's notes, collapsed by default but always one click away and editable. */
export function BacklogAgentNotes({ notes, onSave }: BacklogAgentNotesProps) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const panel = useId();
	return (
		<div>
			<button
				type="button"
				aria-expanded={open}
				aria-controls={panel}
				onClick={() => setOpen((value) => !value)}
				className="flex items-center gap-1 text-xs font-medium text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
			>
				<ChevronRight aria-hidden="true" className={cn("size-3 transition-transform motion-reduce:transition-none", open && "rotate-90")} />
				{t("backlog.detail.agentNotes")}
			</button>
			<div id={panel} hidden={!open} className="mt-1 pl-4">
				<BacklogEditableText label={t("backlog.detail.agentNotes")} value={notes} multiline markdown maxLength={20000} emptyLabel={t("backlog.agentNotes.empty")} onSave={onSave} />
			</div>
		</div>
	);
}
