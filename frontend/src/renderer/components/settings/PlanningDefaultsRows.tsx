import { useTranslation } from "react-i18next";
import { useSettings } from "../../hooks/useSettings";
import { usePlannerLocks } from "../../hooks/usePlannerLocks";
import { useUpdateWorkflowDefaults } from "../../hooks/useUpdateWorkflowDefaults";
import { PLANNING_MODES, planningModeKey } from "../../lib/project-local-fields";
import { Switch } from "../ui/switch";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";

/**
 * Settings, Reviews, Planning: the app defaults for planning mode and plan
 * auto-approval that projects without their own value inherit. A managed policy
 * lock shows its value read-only.
 */
export function PlanningDefaultsRows() {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = useUpdateWorkflowDefaults();
	const locks = usePlannerLocks();
	const disabled = isLoading || saving || !settings;
	const mode = settings?.workflowDefaults?.planningMode ?? "auto";
	const autoApprove = settings?.workflowDefaults?.planAutoApprove ?? false;
	const modeLabel = t("settings.reviews.planningMode.label");
	const autoApproveLabel = t("settings.reviews.planAutoApprove.label");
	const managed = (value: string) => (
		<span data-testid="planning-default-managed" className="text-sm text-settings-muted">
			{t("settings.project.autoReview.managed", { value })}
		</span>
	);
	return (
		<>
			<SettingsRow label={modeLabel} description={t("settings.reviews.planningMode.description")}>
				{locks.mode ? (
					managed(t(planningModeKey(locks.mode)))
				) : (
					<SettingsOptionMenu
						aria-label={modeLabel}
						value={mode}
						options={PLANNING_MODES.map((m) => ({ value: m, label: t(planningModeKey(m)) }))}
						triggerClassName="w-fit"
						disabled={disabled}
						onChange={(planningMode) => update({ planningMode: planningMode as "auto" | "always" | "off" })}
					/>
				)}
			</SettingsRow>
			<SettingsRow label={autoApproveLabel} description={t("settings.reviews.planAutoApprove.description")}>
				{locks.autoApproveOff ? (
					managed(t("settings.project.autoReview.off"))
				) : (
					<Switch aria-label={autoApproveLabel} checked={autoApprove} disabled={disabled} onCheckedChange={(planAutoApprove) => update({ planAutoApprove })} />
				)}
			</SettingsRow>
			{error ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</>
	);
}
