import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { commitLabelKey, commitProjectSkills, pushProjectSkillsBranch, type CommitResult, type CommitTarget } from "../lib/skills-commit";
import { projectSkillsQueryKey } from "../lib/skills-offer";
import { cn } from "../lib/utils";
import { RestartOrchestratorButton } from "./RestartOrchestratorButton";
import { Button } from "./ui/button";

type State = { s: "idle" } | { s: "working" } | { s: "done"; res: CommitResult } | { s: "error"; message: string };
type BranchState = { s: "idle" } | { s: "working" } | { s: "done"; remote: string; branch: string } | { s: "error"; message: string };

const message = (err: unknown) => (err instanceof Error ? err.message : String(err));

/**
 * The primary "Commit to <branch>" (or "Commit and push to <remote>/<branch>")
 * action of the skills offer. collect returns the exact paths to commit (it may
 * create them first); nothing else in the checkout is staged or committed.
 * After a commit that reaches sessions it offers to restart the orchestrator;
 * after a refused push it keeps the commit and offers a branch for a PR.
 */
export function SkillsCommitPanel({ projectId, target, preview, create, collect, muted = "text-passive" }: {
	projectId: string;
	target: CommitTarget | undefined;
	/** Paths shown under the button before the click. */
	preview: readonly string[];
	/** True when the click creates the files first. */
	create: boolean;
	collect: () => Promise<string[]>;
	muted?: string;
}) {
	const { t } = useTranslation();
	const client = useQueryClient();
	const [state, setState] = useState<State>({ s: "idle" });
	const [pr, setPr] = useState<BranchState>({ s: "idle" });
	if (!target) return <p className={cn("text-xs leading-4", muted)}>{t("skillsCommit.noTarget")}</p>;
	const run = async () => {
		setState({ s: "working" });
		try {
			const paths = await collect();
			setState({ s: "done", res: await commitProjectSkills(projectId, paths) });
		} catch (err) {
			setState({ s: "error", message: message(err) });
		} finally {
			await client.invalidateQueries({ queryKey: projectSkillsQueryKey(projectId) });
		}
	};
	const pushBranch = async (sha: string) => {
		setPr({ s: "working" });
		try {
			const data = await pushProjectSkillsBranch(projectId, sha);
			setPr({ s: "done", remote: data.remote, branch: data.branch });
		} catch (err) {
			setPr({ s: "error", message: message(err) });
		}
	};
	if (state.s === "done") {
		const { res } = state;
		const sha = res.sha.slice(0, 8);
		const reached = !res.needsPush || res.pushed;
		if (reached) {
			return <div className="flex flex-col gap-1.5" data-testid="skills-commit-result">
				<p role="status" className="text-xs leading-4 text-foreground">{t(res.pushed ? "skillsCommit.pushed" : "skillsCommit.done", { sha, remote: res.remote, branch: res.branch })}</p>
				<p className={cn("text-xs leading-4", muted)}>{t("skillsCommit.pickup")}</p>
				<div><RestartOrchestratorButton projectId={projectId} /></div>
			</div>;
		}
		return <div className="flex flex-col gap-1.5" data-testid="skills-commit-result">
			<p role="alert" className="text-xs leading-4 text-error">{t("skillsCommit.pushFailed", { remote: res.remote, branch: res.branch, error: res.pushError ?? "" })}</p>
			<p className={cn("text-xs leading-4", muted)}>{t("skillsCommit.kept", { sha, branch: res.localUpdated ? res.branch : res.sideBranch ?? res.branch })}</p>
			{pr.s === "done"
				? <p role="status" className="text-xs leading-4 text-foreground">{t("skillsCommit.pushedBranch", { remote: pr.remote, branch: pr.branch, base: res.branch })}</p>
				: <div><Button size="sm" variant="ghost" type="button" disabled={pr.s === "working"} onClick={() => void pushBranch(res.sha)}>{t("skillsCommit.pushBranch")}</Button></div>}
			{pr.s === "error" && <p role="alert" className="text-xs leading-4 text-error">{pr.message}</p>}
		</div>;
	}
	return <div className="flex flex-col gap-1.5">
		<div><Button size="sm" type="button" disabled={state.s === "working" || preview.length === 0} onClick={() => void run()}>
			{state.s === "working" ? t("skillsCommit.working") : t(commitLabelKey(target, create), { remote: target.remote, branch: target.branch })}
		</Button></div>
		{preview.length > 0 && <p className={cn("font-mono text-[11px] leading-4", muted)}>{t("skillsCommit.willCommit", { paths: preview.join(", ") })}</p>}
		<p className={cn("text-xs leading-4", muted)}>{t(target.needsPush ? "skillsCommit.note" : "skillsCommit.noteLocal", { remote: target.remote, branch: target.branch })}</p>
		{state.s === "error" && <p role="alert" className="text-xs leading-4 text-error">{state.message}</p>}
	</div>;
}
