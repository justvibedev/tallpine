import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { fetchGitHubAccountCheck, githubAccountCheckKey } from "../lib/github-account-check";
import { Button } from "./ui/button";

/**
 * Shows which GitHub login the project's token really is (the answer of
 * `gh api user`) under the account override, and warns when it is another
 * account than the one chosen or cannot see the repository.
 */
export function ProjectGitHubAccountCheck({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const query = useQuery({ queryKey: githubAccountCheckKey(projectId), queryFn: () => fetchGitHubAccountCheck(projectId), retry: false });
	const d = query.data;
	if (!d && !query.isFetching && !query.isError) return null;
	const warn = d && ["mismatch", "no_repo_access", "no_token"].includes(d.status);
	const text = !d
		? query.isError ? t("settings.project.githubAccountCheck.error") : t("settings.project.githubAccountCheck.checking")
		: t(`settings.project.githubAccountCheck.${d.status}`, { login: d.login, tokenLogin: d.tokenLogin, repo: d.repo });
	return <div role={warn ? "alert" : "status"} data-testid="github-account-check" className={`flex items-center justify-between gap-2 px-(--settings-row-inset) pb-2 text-xs ${warn ? "text-warning" : "text-passive"}`}>
		<span>{text}</span>
		<Button size="sm" type="button" variant="ghost" disabled={query.isFetching} onClick={() => void query.refetch()}>{t("settings.project.githubAccountCheck.recheck")}</Button>
	</div>;
}
