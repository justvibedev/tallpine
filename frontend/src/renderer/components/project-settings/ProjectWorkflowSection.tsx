import { useTranslation } from "react-i18next";
import { PLANNING_MODES, planningModeKey, type ProjectLocalFieldsDraft } from "../../lib/project-local-fields";
import { ProjectPlanAutoApproveRow } from "../settings/ProjectAgentRoleControls";
import { SettingsOptionMenu } from "../settings/SettingsOptionMenu";
import { SettingsInlineInput, SettingsRow } from "../settings/SettingsRow";

type Patch = (patch: Partial<ProjectLocalFieldsDraft>) => void;

/** What a project without its own value inherits, and what the organization locks. */
export interface ProjectWorkflowInherit {
	appPlanningMode: string;
	appPlanAutoApprove: boolean;
	/** The managed planning mode lock, "" when none. */
	lockedPlanningMode: string;
	/** Plan auto-approval is locked off by the organization. */
	planAutoApproveLocked: boolean;
}

/** Workflow: when to plan, who approves the plan, and what a worker runs before it pushes. */
export function ProjectWorkflowSection({ values, planAutoApprove, showPlanAutoApprove = true, inherit, onChange, onPlanAutoApprove }: { values: ProjectLocalFieldsDraft; planAutoApprove: boolean | null | undefined; showPlanAutoApprove?: boolean; inherit: ProjectWorkflowInherit; onChange: Patch; onPlanAutoApprove: (value: boolean | null) => void }) {
	const { t } = useTranslation();
	const modeName = (mode: string) => t(planningModeKey(mode));
	// "" inherits the app default; a stored "auto" is the project's own explicit choice.
	const options = [{ value: "", label: t("settings.project.planningMode.inherit", { value: modeName(inherit.appPlanningMode) }) }, ...PLANNING_MODES.map((m) => ({ value: m, label: modeName(m) }))];
	return (
		<>
			<SettingsRow label={t("settings.project.planningModeLabel")} description={t("settings.project.planningModeDescription")}>
				{inherit.lockedPlanningMode ? (
					<span data-testid="project-planning-mode-managed" className="text-sm text-settings-muted">
						{t("settings.project.autoReview.managed", { value: modeName(inherit.lockedPlanningMode) })}
					</span>
				) : (
					<SettingsOptionMenu aria-label={t("settings.project.planningModeLabel")} value={values.planningMode ?? ""} options={options} triggerClassName="w-fit" onChange={(planningMode) => onChange({ planningMode })} />
				)}
			</SettingsRow>
			{showPlanAutoApprove && <ProjectPlanAutoApproveRow value={planAutoApprove} onChange={onPlanAutoApprove} appDefault={inherit.appPlanAutoApprove} locked={inherit.planAutoApproveLocked} />}
			<SettingsRow label={t("settings.project.verifyCommand")} description={t("settings.project.verifyCommandDescription")}>
				<SettingsInlineInput id="project-verify-command" label={t("settings.project.verifyCommand")} value={values.verifyCommand ?? ""} placeholder={t("settings.project.verifyCommandPlaceholder")} onChange={(verifyCommand) => onChange({ verifyCommand })} />
			</SettingsRow>
		</>
	);
}
