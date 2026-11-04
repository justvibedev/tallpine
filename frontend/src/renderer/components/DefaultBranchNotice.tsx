import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { defaultBranchCheckKey, fetchDefaultBranchCheck } from "../lib/default-branch-check";
import { Button } from "./ui/button";

/**
 * Shown in project settings when another branch strictly contains the default
 * branch and is much newer, so every session starts from older code than the
 * repository has. Detection only: the button hands the branch to the settings
 * form (onSwitch), which fills the Default branch field and autosaves it like a
 * typed change, so the form's draft and the stored config never disagree.
 */
export function DefaultBranchNotice({ projectId, onSwitch }: { projectId: string; onSwitch: (branch: string) => void }) {
	const { t } = useTranslation();
	const [switched, setSwitched] = useState(false);
	const query = useQuery({ queryKey: defaultBranchCheckKey(projectId), queryFn: () => fetchDefaultBranchCheck(projectId), retry: false });
	const s = query.data?.suggestion;
	if (!s || switched) return null;
	return <section role="status" data-testid="default-branch-notice" className="mb-3 flex flex-col gap-1.5 rounded-lg border border-border bg-surface px-3 py-2 text-xs leading-4 text-foreground">
		<p>{t("settings.project.defaultBranchNotice.body", { current: query.data?.defaultBranch, branch: s.branch, count: s.ahead, days: s.newerDays })}</p>
		<p className="text-passive">{t(s.checkedOut ? "settings.project.defaultBranchNotice.checkedOut" : "settings.project.defaultBranchNotice.other", { branch: s.branch })}</p>
		<div><Button size="sm" type="button" onClick={() => { onSwitch(s.branch); setSwitched(true); }}>{t("settings.project.defaultBranchNotice.switch", { branch: s.branch })}</Button></div>
	</section>;
}
