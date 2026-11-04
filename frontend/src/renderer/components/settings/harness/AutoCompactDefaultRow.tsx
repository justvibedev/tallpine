import { useTranslation } from "react-i18next";
import { AutoCompactSelect } from "../../chat/AutoCompactRow";
import { supportsAutoCompact } from "../../../lib/auto-compact";
import { useSettings, useUpdateHarnessDefault } from "../../../hooks/useSettings";

/**
 * Where new sessions of one agent compact on their own, saved per agent. A
 * session can pick its own in the model menu. Shown only for agents Tallpine can tell
 * where to compact (Claude Code; Codex terminals).
 */
export function AutoCompactDefaultRow({ agentId, hostId }: { agentId: string; hostId?: string }) {
	const { t } = useTranslation();
	const { settings } = useSettings(hostId);
	const { update, error } = useUpdateHarnessDefault(hostId);
	if (supportsAutoCompact(agentId, "", true) === "no") return null;
	const value = settings?.harnessDefaults?.[agentId]?.autoCompactWindow as Parameters<typeof AutoCompactSelect>[0]["value"];
	return (
		<div className="flex shrink-0 flex-col gap-1">
			<div className="flex items-center gap-3 text-sm text-settings-label">
				<label htmlFor={`auto-compact-default-${agentId}`}>{t("settings.autoCompactDefault.label")}</label>
				<AutoCompactSelect
					id={`auto-compact-default-${agentId}`}
					value={value}
					onChange={(next) => { void update({ agentId, autoCompactWindow: next ?? "" }).catch(() => undefined); }}
				/>
			</div>
			<p className="text-xs text-settings-muted">{t("settings.autoCompactDefault.hint")}</p>
			{error ? <p role="alert" className="text-xs text-error">{error}</p> : null}
		</div>
	);
}
