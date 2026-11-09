import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { fetchGitHubAccounts, githubAccountsKey } from "../../lib/github-accounts";
import { SettingsOptionMenu, type SettingsOption } from "../settings/SettingsOptionMenu";
import { SettingsInlineInput, SettingsRow } from "../settings/SettingsRow";

/**
 * Picks the GitHub account a project's sessions use from the accounts signed in
 * to gh on this machine, or Auto-pick. A pinned login that is no longer signed
 * in stays selectable so the owner can see it and change it.
 */
export function ProjectGitHubAccountRow({ projectId, value, onChange }: { projectId?: string; value: string; onChange: (login: string) => void }) {
	const { t } = useTranslation();
	const query = useQuery({ queryKey: githubAccountsKey(projectId ?? ""), queryFn: () => fetchGitHubAccounts(projectId ?? ""), enabled: Boolean(projectId), retry: false });
	const accounts = query.data?.accounts ?? [];
	const typed = value.trim();
	// A pin stored in another letter case still selects the listed account.
	const pinned = accounts.find((a) => a.login.toLowerCase() === typed.toLowerCase())?.login ?? typed;
	const autoPick = query.data?.autoPick ?? "";
	const options: SettingsOption<string>[] = [
		{ value: "", label: autoPick ? t("settings.project.githubUserAutoNow", { login: autoPick }) : t("settings.project.githubUserAuto") },
		...accounts.map((a) => ({ value: a.login, label: a.canPush === "no" ? t("settings.project.githubUserCannotPush", { login: a.login }) : a.login })),
	];
	if (pinned && !accounts.some((a) => a.login === pinned)) {
		options.push({ value: pinned, label: query.isSuccess && !query.data.unavailable ? t("settings.project.githubUserNotSignedIn", { login: pinned }) : pinned });
	}
	// No local account list (a project on a remote host): the login is typed.
	if (!projectId) return <SettingsRow label={t("settings.project.githubUser")} description={t("settings.project.githubUserDescription")}>
		<SettingsInlineInput id="githubUser" label={t("settings.project.githubUser")} value={value} placeholder={t("settings.project.githubUserAuto")} onChange={onChange} />
	</SettingsRow>;
	const note = query.data?.unavailable
		? t("settings.project.githubUserNoGh")
		: query.isSuccess && accounts.length === 0 ? t("settings.project.githubUserNoneSignedIn") : "";
	return <SettingsRow label={t("settings.project.githubUser")} description={<>{t("settings.project.githubUserDescription")}{note && <> {note}</>}</>}>
		<SettingsOptionMenu aria-label={t("settings.project.githubUser")} value={pinned} options={options} triggerClassName="w-fit" onChange={onChange} />
	</SettingsRow>;
}
