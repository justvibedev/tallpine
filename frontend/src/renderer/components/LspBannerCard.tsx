import { useQuery } from "@tanstack/react-query";
import { Code2, EllipsisVertical } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { fetchProjectSkills, lspBannerReason, lspBannerResolved, lspEnabledMissing, projectSkillsQueryKey, resolveLspBanner } from "../lib/skills-offer";
import { useUiStore } from "../stores/ui-store";
import { Button } from "./ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";

/**
 * One-time banner pointing at Settings > Code intelligence when the project has
 * an LSP recommendation or mismatch. Review, Not now and Don't ask again all
 * resolve it for good for that project. A plugin switched on while its plugin
 * or server is missing is a reminder instead: an earlier dismissal does not
 * hide it, and dismissing it hides it only until the banner mounts again.
 */
export function LspBannerCard({ projectId }: { projectId: string }) {
	// Keyed so one project's resolved state never carries into another.
	return <LspBannerBody key={projectId} projectId={projectId} />;
}

function LspBannerBody({ projectId }: { projectId: string }) {
	const { t } = useTranslation();
	const query = useQuery({ queryKey: projectSkillsQueryKey(projectId), queryFn: () => fetchProjectSkills(projectId), retry: false });
	const [resolved, setResolved] = useState(() => lspBannerResolved(projectId));
	const [dismissed, setDismissed] = useState(false);
	const reason = lspBannerReason(query.data?.lsp);
	const reminder = reason === "enabledMissing";
	if (!reason || dismissed || (resolved && !reminder)) return null;
	const done = () => { resolveLspBanner(projectId); setResolved(true); setDismissed(true); };
	const line = reminder
		? t("lspBanner.enabledMissing", { plugin: lspEnabledMissing(query.data?.lsp)?.pluginId ?? "" })
		: t("lspBanner.line", { detail: t(`lspBanner.detail.${reason}`) });
	return <section aria-label={line} data-testid="lsp-banner" className="flex items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-xs text-foreground">
		<Code2 aria-hidden="true" className="size-3.5 shrink-0 text-passive" />
		<p className="min-w-0 flex-1 leading-4">{line}</p>
		<Button size="sm" variant="ghost" type="button" onClick={() => { done(); useUiStore.getState().openProjectSettings(projectId, { section: "codeIntelligence" }); }}>{t("skillsNotice.review")}</Button>
		<Button size="sm" variant="ghost" type="button" onClick={done}>{t("skillsNotice.notNow")}</Button>
		{reminder ? null : <DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button size="icon-sm" variant="ghost" type="button" aria-label={t("skillsNotice.moreOptions")} title={t("skillsNotice.moreOptions")}><EllipsisVertical className="size-3.5" aria-hidden="true" /></Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end"><DropdownMenuItem onSelect={done}>{t("skillsNotice.never")}</DropdownMenuItem></DropdownMenuContent>
		</DropdownMenu>}
	</section>;
}
