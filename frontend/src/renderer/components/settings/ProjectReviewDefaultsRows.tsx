import { useTranslation } from "react-i18next";
import { REVIEW_RISK_DEFAULTS, splitReviewRiskPaths, type ReviewDefaultsDraft } from "../../lib/project-review-defaults";
import { ProjectToggleRow } from "./ProjectAgentRoleControls";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsInlineInput, SettingsRow } from "./SettingsRow";

type Patch = (patch: Partial<ReviewDefaultsDraft>) => void;

/**
 * Auto review is three-way: inherit the app default (Settings, Reviews) or set
 * On or Off for this project. A managed lock shows its value read-only.
 */
export function ProjectAutoReviewRow({ values, onChange, description, appDefault = false, locked = false }: { values: ReviewDefaultsDraft; onChange: Patch; description?: string; appDefault?: boolean; locked?: boolean }) {
	const { t } = useTranslation();
	const label = t("settings.project.autoReviewToggle");
	const onOff = (on: boolean) => t(on ? "settings.project.autoReview.on" : "settings.project.autoReview.off");
	const choice = values.autoReview == null ? "inherit" : values.autoReview ? "on" : "off";
	const options: SettingsOption<"inherit" | "on" | "off">[] = [
		{ value: "inherit", label: t("settings.project.autoReview.inherit", { value: onOff(appDefault) }) },
		{ value: "on", label: onOff(true) },
		{ value: "off", label: onOff(false) },
	];
	return (
		<SettingsRow label={label} description={description ?? t("settings.project.autoReviewDescription")}>
			{locked ? (
				<span data-testid="project-auto-review-managed" className="text-sm text-settings-muted">
					{t("settings.project.autoReview.managed", { value: onOff(values.autoReview ?? appDefault) })}
				</span>
			) : (
				<SettingsOptionMenu aria-label={label} value={choice} options={options} onChange={(next) => onChange({ autoReview: next === "inherit" ? null : next === "on" })} />
			)}
		</SettingsRow>
	);
}

export function ProjectTerminateOnMergeRow({ values, onChange }: { values: ReviewDefaultsDraft; onChange: Patch }) {
	const { t } = useTranslation();
	return <ProjectToggleRow id="project-terminate-on-merge" label={t("settings.reviewDefaults.terminateOnMerge")} description={t("settings.reviewDefaults.terminateOnMergeDescription")} checked={values.terminateOnPrMerge ?? true} onCheckedChange={(terminateOnPrMerge) => onChange({ terminateOnPrMerge })} />;
}

/** The paths that earn a PR the stronger reviewer, the list in effect, and whether it is the built-in one. */
export function ProjectRiskPathsRows({ values, onChange }: { values: ReviewDefaultsDraft; onChange: Patch }) {
	const { t } = useTranslation();
	const own = splitReviewRiskPaths(values.reviewRiskPaths);
	const builtIn = own.length === 0;
	const effective = builtIn ? [...REVIEW_RISK_DEFAULTS.paths] : own;
	return (
		<>
			<SettingsRow label={t("settings.reviewDefaults.riskPaths")} description={t("settings.reviewDefaults.riskPathsDescription")}>
				<SettingsInlineInput
					id="project-review-risk-paths"
					label={t("settings.reviewDefaults.riskPaths")}
					value={values.reviewRiskPaths ?? ""}
					onChange={(reviewRiskPaths) => onChange({ reviewRiskPaths })}
					placeholder={t("settings.reviewDefaults.riskPathsPlaceholder")}
				/>
			</SettingsRow>
			<p className="px-(--settings-row-inset) pb-2 text-xs leading-row text-settings-muted" data-testid="effective-risk-paths">
				{t("settings.reviewDefaults.effectivePaths", { paths: effective.join(", ") })}
				{builtIn ? <span className="ml-1.5 rounded-sm bg-settings-hover px-1 py-px">{t("settings.reviewDefaults.builtIn")}</span> : null}
			</p>
			<ProjectToggleRow id="project-review-first-only" label={t("settings.reviewDefaults.firstReviewOnly")} description={t("settings.reviewDefaults.firstReviewOnlyDescription")} checked={values.reviewRiskFirstOnly ?? false} onCheckedChange={(reviewRiskFirstOnly) => onChange({ reviewRiskFirstOnly })} />
		</>
	);
}
