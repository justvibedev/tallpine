/**
 * The auto-compaction control inside the context details: change this session's
 * window, save it as the agent's default, and see whether it applies now or
 * after a restart (with a one-click restart that keeps the conversation).
 */

import { useTranslation } from "react-i18next";
import type { TurnSettings } from "../../types/conversation";
import { AutoCompactSelect } from "./AutoCompactRow";

export interface CompactionControlProps {
	/** This session's own choice; unset follows the agent's default. */
	value?: TurnSettings["autoCompactWindow"];
	/** The Settings default for this agent, named on the Auto option. */
	defaultChoice?: string;
	/** The running agent still compacts at an older value. */
	pendingRestart: boolean;
	disabled?: boolean;
	onChange: (value: TurnSettings["autoCompactWindow"]) => void;
	/** Saves the session's choice as the agent's default. */
	onSetDefault?: (value: string) => void;
	onRestart?: () => void;
	restarting?: boolean;
	restartBusy?: boolean;
	error?: string;
}

export function CompactionControl(props: CompactionControlProps) {
	const { t } = useTranslation();
	const { value, defaultChoice, pendingRestart, disabled, onChange, onSetDefault, onRestart, restarting, restartBusy, error } = props;
	const canSetDefault = Boolean(value) && value !== defaultChoice && onSetDefault;
	return (
		<div className="mt-3 flex flex-col gap-1.5 border-t border-border pt-3" data-compaction-control="">
			<div className="flex items-center justify-between gap-2">
				<span className="text-muted-foreground">{t("contextMeter.compaction.label")}</span>
				<AutoCompactSelect value={value} defaultChoice={defaultChoice} disabled={disabled} onChange={onChange} className="flex items-center gap-1" />
			</div>
			<div className="flex items-center justify-between gap-2">
				<span role="status" className={pendingRestart ? "text-status-needs-you" : "text-muted-foreground"}>
					{pendingRestart ? t("contextMeter.compaction.appliesAfterRestart") : t("contextMeter.compaction.appliesNow")}
				</span>
				{canSetDefault ? (
					<button type="button" onClick={() => onSetDefault(value as string)} className="rounded text-foreground underline decoration-dashed underline-offset-2 hover:decoration-solid focus-visible:outline-2 focus-visible:outline-ring">
						{t("contextMeter.compaction.setDefault")}
					</button>
				) : null}
			</div>
			{pendingRestart && onRestart ? (
				<button
					type="button"
					disabled={restarting}
					onClick={onRestart}
					className="self-start rounded-md bg-interactive-active px-2 py-1 text-foreground transition-colors duration-fast hover:bg-interactive-hover focus-visible:outline-2 focus-visible:outline-ring disabled:cursor-default disabled:opacity-60"
				>
					{restarting ? t("contextMeter.compaction.restarting") : t("contextMeter.compaction.restartNow")}
				</button>
			) : null}
			{restartBusy ? <p role="alert" className="text-muted-foreground">{t("contextMeter.compaction.busy")}</p> : error ? <p role="alert" className="text-error">{error}</p> : null}
		</div>
	);
}
