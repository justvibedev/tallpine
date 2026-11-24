import { useState } from "react";
import { useTranslation } from "react-i18next";
import { type DefaultRole, type RoleDefaultApplyResult, useRoleDefaultApply } from "../../hooks/useRoleDefaultApply";
import { agentLabel } from "../../lib/agent-options";
import { Button } from "../ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { SettingsRow } from "./SettingsRow";

type RoleValue = { agent: string; model: string; effort: string };

/**
 * Settings, Reviews: brings every project whose role runs with something other
 * than the app default in line with it (B847). The dialog lists those projects
 * first; nothing changes until the owner confirms.
 */
export function RoleDefaultApplyRow({ role, disabled }: { role: DefaultRole; disabled: boolean }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	return (
		<>
			<SettingsRow label={t("settings.reviews.applyDefault.label")} description={t("settings.reviews.applyDefault.description")}>
				<Button type="button" variant="footer" data-testid={`apply-default-${role}`} disabled={disabled} onClick={() => setOpen(true)}>
					{t("settings.reviews.applyDefault.button")}
				</Button>
			</SettingsRow>
			{open ? <RoleDefaultApplyDialog role={role} onClose={() => setOpen(false)} /> : null}
		</>
	);
}

function RoleDefaultApplyDialog({ role, onClose }: { role: DefaultRole; onClose: () => void }) {
	const { t } = useTranslation();
	const { report, loading, loadError, apply, applying, applyError } = useRoleDefaultApply(role, true);
	const [result, setResult] = useState<RoleDefaultApplyResult>();
	const projects = report?.projects ?? [];
	const describe = (v: RoleValue) =>
		[agentLabel(v.agent), v.model || t("settings.reviews.applyDefault.agentDefault"), v.effort || t("settings.reviews.applyDefault.agentDefault")].join(" · ");
	const confirm = () =>
		void apply(projects.map((p) => p.projectId)).then((r) => {
			if (r.failed.length === 0) onClose();
			else setResult(r);
		}, () => undefined);
	return (
		<Dialog open onOpenChange={(next) => { if (!next) onClose(); }}>
			<DialogContent className={settingsDialogContentClass} showCloseButton={false}>
				<div className={settingsDialogHeaderClass}>
					<DialogTitle className="settings-dialog-title">{t("settings.reviews.applyDefault.title")}</DialogTitle>
					<DialogDescription>
						{report ? t("settings.reviews.applyDefault.body", { value: describe(report.default) }) : null}
					</DialogDescription>
				</div>
				<div className={settingsDialogBodyClass}>
					{loading ? <p className="text-sm text-settings-muted" role="status">{t("settings.reviews.applyDefault.loading")}</p> : null}
					{report && projects.length === 0 ? <p className="text-sm text-settings-muted" role="status">{t("settings.reviews.applyDefault.none")}</p> : null}
					{projects.length > 0 ? (
						<ul className="flex max-h-64 flex-col overflow-y-auto" data-testid={`apply-default-${role}-projects`}>
							{projects.map((p) => (
								<li key={p.projectId} className="flex min-w-0 items-baseline justify-between gap-3 py-1 text-sm">
									<span className="truncate text-foreground">{p.name}</span>
									<span className="shrink-0 truncate text-settings-muted">{describe(p.current)}</span>
								</li>
							))}
						</ul>
					) : null}
					{result?.failed.map((f) => (
						<p key={f.projectId} className="text-sm text-destructive" role="alert">
							{t("settings.reviews.applyDefault.failed", { project: f.projectId, error: f.error })}
						</p>
					))}
					{loadError || applyError ? <p className="text-sm text-destructive" role="alert">{loadError ?? applyError}</p> : null}
				</div>
				<div className={settingsDialogFooterClass}>
					<DialogClose asChild>
						<Button type="button" variant="ghost">{t("common.cancel", "Cancel")}</Button>
					</DialogClose>
					<Button type="button" data-testid={`apply-default-${role}-confirm`} disabled={applying || projects.length === 0 || result !== undefined} onClick={confirm}>
						{t("settings.reviews.applyDefault.confirm")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
