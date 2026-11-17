import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ProjectSettingsSection } from "@tallpine/product-ui";
import type { components } from "../../../api/schema";
import type { ClaudePlugins, ProjectLocalFieldsDraft } from "../../lib/project-local-fields";
import { useLspInstall, type LspInstallTarget } from "../../lib/lsp-install";
import { lspRecInstallable, lspRecMissing } from "../../lib/skills-offer";
import { Button } from "../ui/button";
import { withPluginSwitch } from "./InstructionsAndSkillsClaude";
import { LspInstallButton, LspInstallStatus } from "./LspInstall";
import { roleValues, SKILL_ROLES, SkillRoleHeader, SkillRoleRow, type SkillRole } from "./SkillRoleRow";

type Lsp = components["schemas"]["ControllersProjectLSP"];
type Rec = Lsp["recommendations"][number];
type Patch = (patch: Partial<ProjectLocalFieldsDraft>) => void;

/** The plugin map with one plugin set for several roles at once. */
export function withPluginRoles(plugins: ClaudePlugins | undefined, roles: readonly string[], id: string, on: boolean): ClaudePlugins {
	return roles.reduce<ClaudePlugins>((acc, role) => withPluginSwitch(acc, role as SkillRole, id, on), plugins ?? {});
}

function CopyLine({ label, command, action }: { label: string; command: string; action?: ReactNode }) {
	const { t } = useTranslation();
	const [copied, setCopied] = useState(false);
	return <div className="flex items-center justify-between gap-3">
		<div className="min-w-0">
			<p className="text-xs leading-4 text-settings-muted">{label}</p>
			<code className="block truncate font-mono text-xs leading-4 text-settings-label" title={command}>{command}</code>
		</div>
		<div className="flex shrink-0 items-center gap-1.5">
			{action}
			<Button size="sm" variant="ghost" type="button" aria-label={t("settings.instructions.lsp.copyLabel", { label })}
				onClick={() => { void navigator.clipboard?.writeText(command).then(() => setCopied(true)); }}>
				{copied ? t("settings.instructions.lsp.copied") : t("settings.instructions.lsp.copy")}
			</Button>
		</div>
	</div>;
}

/**
 * The LSP plugin that matches the repo's main languages, whether its plugin
 * and language server are installed, and how to install them: an Install
 * button that runs the daemon's fixed command (with a project id; a remote
 * project has none) and the command to copy. Switches are the existing
 * per-role claudePlugins; nothing is on by default. Switching a plugin on
 * installs whatever of it is missing; a plugin that is on while something is
 * missing gets a warning row. Nothing is installed for a plugin that is off.
 */
export function InstructionsAndSkillsLsp({ projectId, ...props }: LspProps & { projectId?: string }) {
	return projectId === undefined ? <LspSection {...props} /> : <LspSectionWithInstall {...props} projectId={projectId} />;
}

type LspProps = { values: ProjectLocalFieldsDraft; lsp: Lsp | undefined; onChange: Patch };
type Install = ReturnType<typeof useLspInstall>;

function LspSectionWithInstall({ projectId, ...props }: LspProps & { projectId: string }) {
	return <LspSection {...props} install={useLspInstall(projectId)} />;
}

function LspSection({ values, lsp, onChange, install }: LspProps & { install?: Install }) {
	const { t } = useTranslation();
	const installButton = (language: string, target: LspInstallTarget, label: string) => (install === undefined ? null : <LspInstallButton install={install} language={language} target={target} label={label} />);
	const overrides: ClaudePlugins = values.claudePlugins ?? {};
	if (lsp === undefined) return null;
	/** Applies a switch change; turning the plugin on installs what it lacks. */
	const switchPlugin = (rec: Rec, claudePlugins: ClaudePlugins, on: boolean) => {
		onChange({ claudePlugins });
		if (on && install !== undefined && !install.running && lspRecInstallable(rec)) install.start({ language: rec.language, target: "all" });
	};
	return <ProjectSettingsSection title={t("settings.instructions.lsp.title")} grouped>
		<p className="px-(--settings-row-inset) pt-2.5 text-xs leading-4 text-settings-muted">{t("settings.instructions.lsp.note")}</p>
		{lsp.recommendations.length === 0 && lsp.mismatches.length === 0
			? <p className="px-(--settings-row-inset) py-2.5 text-sm text-settings-muted">{t("settings.instructions.lsp.none")}</p>
			: null}
		{lsp.recommendations.length > 0 && <SkillRoleHeader />}
		{lsp.recommendations.map((rec) => ({ rec, values: roleValues(rec.roles, (role) => overrides[role]?.[rec.pluginId], () => false) })).map(({ rec, values: switches }) => <div key={rec.pluginId} data-testid={`lsp-recommendation-${rec.language}`}>
			<SkillRoleRow id={rec.pluginId} name={rec.pluginId}
				description={t("settings.instructions.lsp.detected", { language: rec.language, count: rec.files })}
				note={[rec.pluginInstalled ? t("settings.instructions.lsp.pluginReady") : t("settings.instructions.lsp.pluginMissing"),
					rec.binaryInstalled && rec.binaryProblem === "" ? t("settings.instructions.lsp.binaryReady", { binary: rec.binary }) : t("settings.instructions.lsp.binaryMissing", { binary: rec.binary })].join(" · ")}
				values={switches}
				onChange={(role, on) => switchPlugin(rec, withPluginSwitch(overrides, role, rec.pluginId, on), on)} />
			<div className="flex flex-col gap-1.5 px-(--settings-row-inset) pb-2.5">
				<EnabledMissing rec={rec} roles={SKILL_ROLES.filter((role) => switches[role].on)} action={lspRecInstallable(rec) ? installButton(rec.language, "all", rec.pluginId) : null} />
				{rec.binaryInstalled && rec.binaryProblem !== "" ? <p role="alert" data-testid={`lsp-binary-problem-${rec.language}`} className="text-xs leading-4 text-warning">{t(`settings.instructions.lsp.problem.${rec.binaryProblem as "no-go" | "no-tsserver"}`)}</p> : null}
				{rec.pluginInstalled ? null : <CopyLine label={t("settings.instructions.lsp.installPlugin")} command={rec.pluginInstall}
					action={installButton(rec.language, "plugin", t("settings.instructions.lsp.installPlugin"))} />}
				{rec.binaryProblem === "no-go" && rec.toolchainInstall !== "" ? <CopyLine label={t("settings.instructions.lsp.installGo")} command={rec.toolchainInstall}
					action={rec.toolchainInstallable ? installButton(rec.language, "go", t("settings.instructions.lsp.installGo")) : null} /> : null}
				{rec.binaryInstalled && rec.binaryProblem !== "no-tsserver" ? null : <CopyLine label={t("settings.instructions.lsp.installBinary", { binary: rec.binary })} command={rec.binaryInstall}
					action={rec.binaryInstallable ? installButton(rec.language, "binary", t("settings.instructions.lsp.installBinary", { binary: rec.binary })) : null} />}
				{install === undefined ? null : <LspInstallStatus install={install} language={rec.language} />}
				{switches.worker.on && switches.reviewer.on ? null : <div><Button size="sm" variant="outline" type="button" onClick={() => switchPlugin(rec, withPluginRoles(overrides, ["worker", "reviewer"], rec.pluginId, true), true)}>{t("settings.instructions.lsp.enableWorkers")}</Button></div>}
			</div>
		</div>)}
		{lsp.mismatches.map((mis) => ({ ...mis, roles: mis.roles.filter((role) => overrides[role]?.[mis.pluginId] !== false) })).filter((mis) => mis.roles.length > 0).map((mis) => <div key={mis.pluginId} className="settings-row-bar items-center justify-between gap-3 py-2.5" data-testid={`lsp-mismatch-${mis.language}`}>
			<p className="min-w-0 text-xs leading-4 text-settings-muted">{t("settings.instructions.lsp.mismatch", { plugin: mis.pluginId, language: mis.language, roles: mis.roles.map((role) => t(`settings.instructions.role.${role as SkillRole}`)).join(", ") })}</p>
			<Button size="sm" variant="outline" type="button" onClick={() => onChange({ claudePlugins: withPluginRoles(overrides, mis.roles, mis.pluginId, false) })}>{t("settings.instructions.lsp.turnOff")}</Button>
		</div>)}
	</ProjectSettingsSection>;
}

/** A plugin switched on for some role while its plugin or language server is missing: sessions get no code intelligence until it is installed. */
function EnabledMissing({ rec, roles, action }: { rec: Rec; roles: readonly SkillRole[]; action: ReactNode }) {
	const { t } = useTranslation();
	if (roles.length === 0 || !lspRecMissing(rec)) return null;
	const broken = rec.pluginInstalled && rec.binaryInstalled;
	const missing = [rec.pluginInstalled ? null : rec.pluginId, rec.binaryInstalled && !broken ? null : rec.binary].filter(Boolean).join(" + ");
	return <div role="alert" data-testid={`lsp-enabled-missing-${rec.pluginId}`} className="flex items-center justify-between gap-3">
		<p className="min-w-0 text-xs leading-4 text-warning">{t(broken ? "settings.instructions.lsp.enabledBroken" : "settings.instructions.lsp.enabledMissing", { roles: roles.map((role) => t(`settings.instructions.role.${role}`)).join(", "), missing })}</p>
		{action}
	</div>;
}
