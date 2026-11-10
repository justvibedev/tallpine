import { useTranslation } from "react-i18next";
import { useAgentReadinessQuery } from "../../hooks/useAgentReadinessQuery";
import { useSettings } from "../../hooks/useSettings";
import { useUpdateWorkflowDefaults } from "../../hooks/useUpdateWorkflowDefaults";
import { ReviewerSelect } from "../ReviewerSelect";
import { AgentModelField } from "./ProjectAgentRoleControls";
import { SettingsRow } from "./SettingsRow";

/**
 * Settings, Reviews: the reviewer agent and model a project inherits when it
 * sets no reviewer of its own. Empty keeps each project's worker-derived reviewer.
 */
export function DefaultReviewerRows() {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = useUpdateWorkflowDefaults();
	const agentsQuery = useAgentReadinessQuery();
	const harness = settings?.workflowDefaults?.reviewerHarness ?? "";
	const model = settings?.workflowDefaults?.reviewerModel ?? "";
	const disabled = isLoading || saving || !settings;
	return (
		<>
			<SettingsRow label={t("settings.reviews.defaultReviewer.label")} description={t("settings.reviews.defaultReviewer.description")}>
				<ReviewerSelect
					value={harness}
					model={model}
					harnessOnly
					defaultHarness=""
					emptyLabel={t("settings.reviews.defaultReviewer.workerDerived")}
					triggerClassName="w-full"
					ariaLabel={t("settings.reviews.defaultReviewer.label")}
					agents={agentsQuery.data?.agents}
					disabled={disabled}
					// Re-picking the current harness changes nothing; the daemon drops the model when the harness really changes.
					onChange={(reviewerHarness) => reviewerHarness !== harness && update({ reviewerHarness })}
				/>
			</SettingsRow>
			{harness !== "" && (
				<SettingsRow label={t("settings.reviews.defaultReviewerModel.label")}>
					<div className="min-w-0">
						<AgentModelField
							role="reviewer"
							ariaLabel={t("settings.reviews.defaultReviewerModel.label")}
							agentId={harness}
							projectId=""
							model={model}
							mode=""
							effort=""
							onModelChange={(reviewerModel) => update({ reviewerModel })}
							onModeChange={() => undefined}
							onValidityChange={() => undefined}
						/>
					</div>
				</SettingsRow>
			)}
			{error ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</>
	);
}
