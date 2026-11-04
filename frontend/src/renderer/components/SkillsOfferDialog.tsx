import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { applyProjectSkillOffer, fetchProjectSkills, groupPendingPaths, offerItemLine, projectSkillsQueryKey, skillsOfferWanted, type SkillOffer } from "../lib/skills-offer";
import { useSkillsOfferStore } from "../stores/skills-offer-store";
import { appliedMessageKey } from "./ProjectSkillsSettings";
import { SkillsCommitPanel } from "./SkillsCommitPanel";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, settingsDialogBodyClass, settingsDialogContentClass, settingsDialogFooterClass, settingsDialogHeaderClass } from "./ui/dialog";

type Result = { state: "pending" } | { state: "done"; message: string } | { state: "error"; message: string };

/** Mounted once in the shell; open for the project the chat card or the project-add toast asked to review. */
export function SkillsOfferDialogHost() {
	const projectId = useSkillsOfferStore((s) => s.reviewing);
	const close = useSkillsOfferStore((s) => s.closeReview);
	// Keyed by project so a reopened dialog starts from that project's current offers.
	return projectId ? <SkillsOfferDialog key={projectId} projectId={projectId} onClose={close} /> : null;
}

export function SkillsOfferDialog({ projectId, onClose }: { projectId: string; onClose: () => void }) {
	const { t } = useTranslation();
	const client = useQueryClient();
	const query = useQuery({ queryKey: projectSkillsQueryKey(projectId), queryFn: () => fetchProjectSkills(projectId), retry: false });
	// Applying an offer empties the list the query returns; keep what was offered
	// (with its "Created" line) until the dialog closes. New ids append.
	const offered = useRef<SkillOffer[]>([]);
	if (query.data && skillsOfferWanted(query.data)) {
		const known = new Set(offered.current.map((o) => o.id));
		const added = query.data.offers.filter((o) => !known.has(o.id));
		if (added.length > 0) offered.current = [...offered.current, ...added];
	}
	const pendingCommit = query.data?.pendingCommit ?? [];
	const pending = groupPendingPaths(pendingCommit);
	const [results, setResults] = useState<Record<string, Result>>({});
	// Once a commit starts, keep its panel (result, restart) after the refetch empties the lists.
	const [commitUsed, setCommitUsed] = useState(false);
	const apply = async (offer: SkillOffer) => {
		setResults((r) => ({ ...r, [offer.id]: { state: "pending" } }));
		try {
			const data = await applyProjectSkillOffer(projectId, offer.id);
			setResults((r) => ({ ...r, [offer.id]: { state: "done", message: t(appliedMessageKey(data?.created)) } }));
		} catch (err) {
			setResults((r) => ({ ...r, [offer.id]: { state: "error", message: err instanceof Error ? err.message : String(err) } }));
		} finally {
			// Every apply, single or Create all, refreshes the card and a reopened dialog.
			await client.invalidateQueries({ queryKey: projectSkillsQueryKey(projectId) });
		}
	};
	const todo = offered.current.filter((o) => results[o.id]?.state !== "done" && results[o.id]?.state !== "pending");
	const createAll = async () => {
		for (const offer of todo) await apply(offer);
	};
	// Create and commit: create every remaining offer, stop on the first failure
	// (never commit half of it), then commit exactly what was created plus the
	// uncommitted files listed here.
	const createForCommit = async (): Promise<string[]> => {
		setCommitUsed(true);
		const created: string[] = [];
		for (const offer of todo) {
			setResults((r) => ({ ...r, [offer.id]: { state: "pending" } }));
			try {
				const data = await applyProjectSkillOffer(projectId, offer.id);
				created.push(...(data?.created ?? []).map((c) => c.path));
				setResults((r) => ({ ...r, [offer.id]: { state: "done", message: t(appliedMessageKey(data?.created)) } }));
			} catch (err) {
				const message = err instanceof Error ? err.message : String(err);
				setResults((r) => ({ ...r, [offer.id]: { state: "error", message } }));
				throw err;
			}
		}
		return [...new Set([...created, ...pendingCommit])];
	};
	const anyDone = Object.values(results).some((r) => r.state === "done");
	const preview = [...new Set([...todo.flatMap((o) => o.items.map((i) => i.path)), ...pendingCommit])];
	const title = offered.current.length > 0 ? t("skillsNotice.title") : t("skillsNotice.titlePending");
	return <Dialog open onOpenChange={(open) => { if (!open) onClose(); }}>
		<DialogContent className={settingsDialogContentClass} data-testid="skills-offer-dialog">
			<DialogHeader className={settingsDialogHeaderClass}>
				<DialogTitle className="text-sm font-medium">{title}</DialogTitle>
				<DialogDescription className="text-xs text-passive">{t("settings.project.skills.offer.note")}</DialogDescription>
			</DialogHeader>
			<div className={settingsDialogBodyClass}>
				<ul className="flex flex-col gap-3">
					{offered.current.map((offer) => {
						const result = results[offer.id];
						return <li key={offer.id} className="flex flex-col gap-1">
							<div className="flex items-center justify-between gap-3">
								<span className="text-xs leading-4 text-foreground">{t(`settings.project.skills.offer.${offer.id}`)}</span>
								<Button size="sm" variant="ghost" type="button" disabled={result?.state === "pending" || result?.state === "done"} onClick={() => void apply(offer)}>{t("settings.project.skills.offer.create")}</Button>
							</div>
							<ul aria-label={t("settings.project.skills.offer.willCreate")} className="flex flex-col font-mono text-[11px] leading-4 text-passive">
								{offer.items.map((item) => <li key={item.path}>{offerItemLine(item, t)}</li>)}
							</ul>
							{result?.state === "done" && <p role="status" className="text-xs text-passive">{result.message}</p>}
							{result?.state === "error" && <p role="alert" className="text-xs text-error">{result.message}</p>}
						</li>;
					})}
				</ul>
				{pendingCommit.length > 0 && <div className="flex flex-col gap-0.5">
					<p className="text-xs leading-4 text-foreground">{t("skillsNotice.pendingList")}</p>
					<ul aria-label={t("skillsNotice.pendingList")} className="text-[11px] leading-4 text-passive">
						{pending.instructions.length > 0 && <li>{t("skillsNotice.pendingInstructions", { files: pending.instructions.join(", ") })}</li>}
						{pending.skills.length > 0 && <li>{t("skillsNotice.pendingSkills", { names: pending.skills.join(", ") })}</li>}
						{pending.other.map((path) => <li key={path} className="font-mono">{path}</li>)}
					</ul>
				</div>}
				{(todo.length > 0 || pendingCommit.length > 0 || anyDone || commitUsed) && <SkillsCommitPanel projectId={projectId} target={query.data?.commitTarget} preview={preview} create={todo.length > 0} collect={createForCommit} />}
			</div>
			<DialogFooter className={settingsDialogFooterClass}>
				{todo.length > 1 && <Button size="sm" variant="ghost" type="button" onClick={() => void createAll()}>{t("skillsNotice.createAll")}</Button>}
				<Button size="sm" variant="ghost" type="button" onClick={onClose}>{t("common.close")}</Button>
			</DialogFooter>
		</DialogContent>
	</Dialog>;
}
