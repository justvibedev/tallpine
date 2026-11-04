import { useTranslation } from "react-i18next";
import type { ProjectLocalFieldsDraft } from "../../lib/project-local-fields";
import { ProjectToggleRow } from "../settings/ProjectAgentRoleControls";
import { SettingsInlineInput, SettingsRow } from "../settings/SettingsRow";

type Patch = (patch: Partial<ProjectLocalFieldsDraft>) => void;

/** Reviews & PRs: when automatic review stops, and when a clean rebase keeps an approval. */
export function ProjectReviewLimitsRows({ values, onChange }: { values: ProjectLocalFieldsDraft; onChange: Patch }) {
	const { t } = useTranslation();
	return (
		<>
			<SettingsRow label={t("settings.project.autoReviewMaxRounds")} description={t("settings.project.autoReviewMaxRoundsDescription")}>
				<SettingsInlineInput id="project-auto-review-max-rounds" label={t("settings.project.autoReviewMaxRounds")} value={values.autoReviewMaxRounds ?? ""} placeholder="0" onChange={(autoReviewMaxRounds) => onChange({ autoReviewMaxRounds: autoReviewMaxRounds.replace(/\D/g, "") })} />
			</SettingsRow>
			<ProjectToggleRow id="project-approve-identical-patch" label={t("settings.project.approveIdenticalPatch")} description={t("settings.project.approveIdenticalPatchDescription")} checked={values.approveIdenticalPatch ?? false} onCheckedChange={(approveIdenticalPatch) => onChange({ approveIdenticalPatch })} />
		</>
	);
}
