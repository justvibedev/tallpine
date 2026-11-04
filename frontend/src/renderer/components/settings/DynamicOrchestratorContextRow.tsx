import { useTranslation } from "react-i18next";
import { useManagedLocks } from "../../hooks/useManagedLocks";
import { useSettings } from "../../hooks/useSettings";
import { useUpdateDynamicOrchestratorContext } from "../../hooks/useUpdateDynamicOrchestratorContext";
import { Switch } from "../ui/switch";
import { AgentPolicyLock } from "./AgentPolicyLock";
import { SettingsRow } from "./SettingsRow";

/**
 * Lets an orchestrator raise its compaction window while it researches a large
 * codebase itself. Daemon-owned; turning it off ends every running research mode.
 */
export function DynamicOrchestratorContextRow() {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = useUpdateDynamicOrchestratorContext();
	const locked = useManagedLocks().has("app.dynamicOrchestratorContext");
	return (
		<div className="flex w-full flex-col">
			<SettingsRow className="rounded-none" label={t("settings.dynamicOrchestratorContext.label")} description={t("settings.dynamicOrchestratorContext.description")}>
				{locked ? <AgentPolicyLock source="managed" /> : null}
				<Switch
					aria-label={t("settings.dynamicOrchestratorContext.label")}
					checked={settings?.dynamicOrchestratorContext ?? false}
					disabled={isLoading || saving || !settings || locked}
					onCheckedChange={update}
				/>
			</SettingsRow>
			{error ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</div>
	);
}
