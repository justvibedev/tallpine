import { useTranslation } from "react-i18next";
import { useSettings } from "../../hooks/useSettings";
import { useUpdateWorkflowDefaults } from "../../hooks/useUpdateWorkflowDefaults";
import { Switch } from "../ui/switch";
import { AgentPolicySection } from "./AgentPolicySection";
import { DefaultReviewerRows } from "./DefaultReviewerRows";
import { PlanningDefaultsRows } from "./PlanningDefaultsRows";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

/**
 * Settings, Reviews: the app-wide defaults projects inherit when they set none
 * of their own, then the planning controls. Daemon-owned; a change applies to
 * sessions created afterwards.
 */
export function ReviewsSettingsSection() {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = useUpdateWorkflowDefaults();
	return (
		<>
			<SettingsSection title={t("settings.reviews")} sectionId="reviews" grouped>
				<SettingsRow label={t("settings.reviews.autoReview.label")} description={t("settings.reviews.autoReview.description")}>
					<Switch
						aria-label={t("settings.reviews.autoReview.label")}
						checked={settings?.workflowDefaults?.autoReview ?? false}
						disabled={isLoading || saving || !settings}
						onCheckedChange={(autoReview) => update({ autoReview })}
					/>
				</SettingsRow>
				<DefaultReviewerRows />
				{error ? (
					<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
						{error}
					</p>
				) : null}
			</SettingsSection>
			<AgentPolicySection scope="planning">
				<PlanningDefaultsRows />
			</AgentPolicySection>
		</>
	);
}
