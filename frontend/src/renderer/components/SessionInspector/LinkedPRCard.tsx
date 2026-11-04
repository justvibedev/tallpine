import { useTranslation } from "react-i18next";
import { ArrowUpRight, GitPullRequest, Info } from "lucide-react";
import type { components } from "../../../api/schema";
import { ProductExternalLink } from "../ProductExternalLink";
import { CopyButton } from "../chat/CopyButton";
import { type SessionPRReference } from "../../hooks/useSessionScmSummary";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

export function isExternalRepository(pr: SessionPRReference, project: components["schemas"]["Project"] | undefined): boolean {
	if (!project) return false;
	const repositories = [project.repo, project.config?.canonicalRepoURL, ...(project.workspaceRepos?.map((repo) => repo.repo) ?? [])];
	const identities = repositories.flatMap((repository) => {
		if (!repository) return [];
		try {
			const url = new URL(repository.replace(/^git@([^:]+):/, "ssh://git@$1/"));
			return [`${url.hostname.toLowerCase()}/${url.pathname.replace(/^\/|\/$|\.git$/g, "").toLowerCase()}`];
		} catch {
			return [];
		}
	});
	return identities.length > 0 && !identities.includes(`${pr.host.toLowerCase()}/${pr.repo.toLowerCase()}`);
}

export function LinkedPRCard({ external, pr }: { external: boolean; pr: SessionPRReference }) {
	const { t } = useTranslation();
	return (
		<article className="min-w-0 w-full rounded-lg border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3 py-2.5">
			<div className="flex min-w-0 items-center justify-between gap-2">
				<ProductExternalLink className="inline-flex min-w-0 items-center gap-1 font-mono text-xs font-medium text-settings-label underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" href={pr.url}>
					<GitPullRequest className="size-icon-sm shrink-0" aria-hidden="true" />
					<span className="truncate">{pr.host}/{pr.repo} {pr.provider === "gitlab" ? "MR" : "PR"} #{pr.number}</span>
					<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" />
				</ProductExternalLink>
				<CopyButton compact label={t("link.copy")} text={pr.url} />
			</div>
			<div className="mt-1.5 flex items-center justify-between gap-2">
				<div className="flex min-w-0 items-center gap-1 text-xs text-settings-muted">
					<span>{t("inspector.reportedByWorker")}</span>
					<Tooltip>
						<TooltipTrigger asChild>
							<button aria-label={t("inspector.linkedPRInfo")} className="inline-flex size-5 shrink-0 items-center justify-center rounded-full hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" type="button">
								<Info aria-hidden="true" className="size-icon-2xs" />
							</button>
						</TooltipTrigger>
						<TooltipContent className="max-w-72 leading-normal text-pretty">{t("inspector.linkedPRInfo")}</TooltipContent>
					</Tooltip>
				</div>
				{external ? <span className="shrink-0 rounded-full border border-(--color-border-settings-input) px-1.5 py-0.5 text-[9px] leading-none text-settings-muted">{t("inspector.externalRepository")}</span> : null}
			</div>
		</article>
	);
}
