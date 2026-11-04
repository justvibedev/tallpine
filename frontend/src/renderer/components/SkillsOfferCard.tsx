import { useQuery } from "@tanstack/react-query";
import { EllipsisVertical, FileText } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { fetchProjectSkills, pendingNames, projectSkillsQueryKey, setSkillsOfferOff, skillsOfferWanted, summarizeOffers } from "../lib/skills-offer";
import { skillsOfferKey, useSkillsOfferStore } from "../stores/skills-offer-store";
import { Button } from "./ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";

/**
 * A compact card in the chat stream when the project's instruction file or
 * skills are invisible to its agents. It never blocks the session and appears at
 * most once per conversation start while the offer is unresolved; Review opens
 * the dialog, "Don't ask again" turns it off for the project (Settings > Skills
 * turns it back on).
 */
export function SkillsOfferCard(props: { projectId: string; sessionId: string }) {
	// Keyed so one project+session's claim and errors never carry into another.
	return <SkillsOfferCardBody key={skillsOfferKey(props.projectId, props.sessionId)} {...props} />;
}

function SkillsOfferCardBody({ projectId, sessionId }: { projectId: string; sessionId: string }) {
	const { t } = useTranslation();
	const query = useQuery({ queryKey: projectSkillsQueryKey(projectId), queryFn: () => fetchProjectSkills(projectId), retry: false });
	const claim = useSkillsOfferStore((s) => s.claim);
	const dismiss = useSkillsOfferStore((s) => s.dismiss);
	const openReview = useSkillsOfferStore((s) => s.openReview);
	const visible = useSkillsOfferStore((s) => s.active.has(skillsOfferKey(projectId, sessionId)));
	const wanted = skillsOfferWanted(query.data);
	useEffect(() => {
		if (wanted) claim(projectId, sessionId);
	}, [wanted, claim, projectId, sessionId]);
	const [neverError, setNeverError] = useState<string | null>(null);
	const never = async () => {
		try {
			await setSkillsOfferOff(projectId, true);
			dismiss(projectId, sessionId);
			void query.refetch();
		} catch (err) {
			setNeverError(err instanceof Error ? err.message : String(err));
		}
	};
	if (!visible || !wanted || !query.data) return null;
	const { agent, file, skillNames } = summarizeOffers(query.data.offers);
	const names = skillNames.join(", ");
	const line = query.data.offers.length === 0
		? t("skillsNotice.pending", { names: pendingNames(query.data.pendingCommit) })
		: skillNames.length > 0 && file
			? t("skillsNotice.card.both", { names, agent, file })
			: skillNames.length > 0
				? t("skillsNotice.card.skills", { names, agent })
				: t("skillsNotice.card.instructions", { agent, file });
	return <section aria-label={line} data-testid="skills-offer-card" className="flex flex-col gap-1">
		<div className="flex items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-xs text-foreground">
			<FileText aria-hidden="true" className="size-3.5 shrink-0 text-passive" />
			<p className="min-w-0 flex-1 leading-4">{line}</p>
			<Button size="sm" variant="ghost" type="button" onClick={() => openReview(projectId)}>{t("skillsNotice.review")}</Button>
			<Button size="sm" variant="ghost" type="button" onClick={() => dismiss(projectId, sessionId)}>{t("skillsNotice.notNow")}</Button>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button size="icon-sm" variant="ghost" type="button" aria-label={t("skillsNotice.moreOptions")} title={t("skillsNotice.moreOptions")}><EllipsisVertical className="size-3.5" aria-hidden="true" /></Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end">
					<DropdownMenuItem onSelect={() => void never()}>{t("skillsNotice.never")}</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
		</div>
		{neverError && <p role="alert" className="text-xs text-error">{neverError}</p>}
	</section>;
}
