import { TriangleAlert, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useArtifactStorage,
	useRetainArtifacts,
	type ArtifactRetentionAction,
	type ArtifactRetentionResponse,
} from "../hooks/useArtifactStorage";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { formatBytes } from "@tallpine/shared/format/bytes";
import { Button } from "./ui/button";
import { Checkbox } from "./ui/checkbox";
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
} from "./ui/dialog";
import { SettingsOptionMenu, type SettingsOption } from "./settings/SettingsOptionMenu";

const WINDOW_DAYS = ["7", "30", "90", "180"] as const;
const CONFIRM_NAMES = 8;

/** Standalone (scratchpad) sessions have no project; the tab names their group STANDALONE_WORKSPACE_ID. */
export const inArtifactGroup = (session: { projectId?: string }, groupId?: string) =>
	!groupId || (groupId === STANDALONE_WORKSPACE_ID ? !session.projectId : session.projectId === groupId);

type View = "list" | "confirm" | "result";

/**
 * Session artifact disk usage: sessions by size, flagged when large, with
 * archive or delete for sessions finished longer ago than the chosen window.
 * Sessions an open backlog item references are marked and cannot be selected.
 * Nothing runs without the user selecting sessions and confirming. Opened
 * from the Artifacts tab, optionally for one project and with every finished
 * session preselected.
 */
export function ArtifactStorageDialog({
	onClose,
	projectId,
	preselectFinished = false,
}: {
	onClose: () => void;
	projectId?: string;
	preselectFinished?: boolean;
}) {
	const { t } = useTranslation();
	const [days, setDays] = useState("30");
	const [view, setView] = useState<View>("list");
	const [action, setAction] = useState<ArtifactRetentionAction>("archive");
	const [selected, setSelected] = useState<Set<string>>(new Set());
	const [outcome, setOutcome] = useState<ArtifactRetentionResponse | null>(null);
	const { report, isLoading, error } = useArtifactStorage(Number(days), view === "list");
	const { retain, saving, error: retainError, reset } = useRetainArtifacts();

	const sessions = (report?.sessions ?? []).filter((session) => inArtifactGroup(session, projectId));
	const preselected = useRef(false);
	useEffect(() => {
		if (!preselectFinished || preselected.current || !report) return;
		preselected.current = true;
		setSelected(new Set(report.sessions.filter((s) => s.eligible && inArtifactGroup(s, projectId)).map((s) => s.sessionId)));
	}, [preselectFinished, projectId, report]);
	const chosen = sessions.filter((session) => selected.has(session.sessionId));
	const chosenByName = [...chosen].sort((a, b) => b.sizeBytes - a.sizeBytes);
	const chosenBytes = chosen.reduce((sum, session) => sum + session.sizeBytes, 0);
	const eligible = sessions.filter((session) => session.eligible);
	const windowOptions: SettingsOption<string>[] = WINDOW_DAYS.map((value) => ({
		value,
		label: t("settings.artifactStorage.days", { days: value }),
	}));

	const toggle = (id: string, on: boolean) =>
		setSelected((current) => {
			const next = new Set(current);
			if (on) next.add(id);
			else next.delete(id);
			return next;
		});

	const begin = (next: ArtifactRetentionAction) => {
		reset();
		setAction(next);
		setView("confirm");
	};

	const run = async () => {
		try {
			setOutcome(await retain({ action, olderThanDays: Number(days), sessionIds: chosen.map((s) => s.sessionId) }));
			setSelected(new Set());
			setView("result");
		} catch {
			// The error renders from the mutation state; stay on the confirmation.
		}
	};

	const failed = outcome?.results.filter((r) => r.status === "failed" || r.status === "skipped").length ?? 0;

	return (
		<Dialog open onOpenChange={(next) => { if (!next && !saving) onClose(); }}>
			<DialogContent className={settingsDialogContentClass} showCloseButton={false}>
				<DialogClose asChild>
					<button aria-label={t("common.close")} className="settings-dialog-close-button settings-close-button" disabled={saving} type="button">
						<X aria-hidden="true" className="size-5" />
					</button>
				</DialogClose>
				<div className={settingsDialogHeaderClass}>
					<DialogTitle className="settings-dialog-title">{t("settings.artifactStorage.title")}</DialogTitle>
					<DialogDescription>{t("settings.artifactStorage.dialogDescription")}</DialogDescription>
				</div>

				<div className={settingsDialogBodyClass}>
					{view === "list" ? (
						<>
							<div className="flex items-center justify-between gap-3">
								<p className="text-sm text-settings-label" role="status">
									{report
										? t("settings.artifactStorage.total", {
												total: formatBytes(report.totalBytes),
												versions: formatBytes(report.versionsBytes),
											})
										: isLoading
											? t("settings.artifactStorage.loading")
											: null}
								</p>
								<div className="flex items-center gap-2">
									<span className="text-sm text-settings-muted">{t("settings.artifactStorage.window")}</span>
									<SettingsOptionMenu
										aria-label={t("settings.artifactStorage.window")}
										value={days}
										options={windowOptions}
										onChange={(next) => { setDays(next); setSelected(new Set()); }}
									/>
								</div>
							</div>
							{error ? <p role="alert" className="text-caption leading-4 text-error">{error}</p> : null}
							{report && sessions.length === 0 ? (
								<p className="text-sm text-settings-muted">{t("settings.artifactStorage.empty")}</p>
							) : null}
							<ul className="flex flex-col divide-y divide-[var(--color-border-settings-dialog-header)]">
								{sessions.map((session) => (
									<li key={session.sessionId} className="flex items-center gap-3 py-2.5">
										<Checkbox
											aria-label={session.displayName || session.sessionId}
											checked={selected.has(session.sessionId)}
											disabled={!session.eligible}
											onCheckedChange={(on) => toggle(session.sessionId, on === true)}
										/>
										<div className="flex min-w-0 flex-1 flex-col">
											<span className="truncate text-sm leading-5 text-settings-label">{session.displayName || session.sessionId}</span>
											<span className="truncate text-caption leading-4 text-settings-muted">
												{session.projectId ? `${session.projectId} · ` : ""}
												{session.protected
													? t("settings.artifactStorage.protected")
													: session.eligible
													? t("settings.artifactStorage.eligible")
													: session.terminated
														? t("settings.artifactStorage.recent")
														: t("settings.artifactStorage.active")}
											</span>
										</div>
										{session.overWarn ? (
											<span className="flex items-center gap-1 text-caption text-warning">
												<TriangleAlert aria-hidden="true" className="size-icon-md" />
												{t("settings.artifactStorage.large")}
											</span>
										) : null}
										<span className="w-20 shrink-0 text-right text-sm tabular-nums text-settings-label">
											{formatBytes(session.sizeBytes)}
										</span>
									</li>
								))}
							</ul>
						</>
					) : null}

					{view === "confirm" ? (
						<>
							<p className="text-sm leading-5 text-settings-label">
								{t(action === "archive" ? "settings.artifactStorage.confirmArchive" : "settings.artifactStorage.confirmDelete", {
									selected: chosen.length,
									size: formatBytes(chosenBytes),
								})}
							</p>
							<ul className="flex flex-col gap-1 text-sm text-settings-label">
								{chosenByName.slice(0, CONFIRM_NAMES).map((session) => (
									<li key={session.sessionId} className="flex justify-between gap-3">
										<span className="truncate">{session.displayName || session.sessionId}</span>
										<span className="shrink-0 tabular-nums">{formatBytes(session.sizeBytes)}</span>
									</li>
								))}
								{chosenByName.length > CONFIRM_NAMES ? (
									<li className="text-settings-muted">{t("settings.artifactStorage.more", { count: chosenByName.length - CONFIRM_NAMES })}</li>
								) : null}
							</ul>
							{retainError ? <p role="alert" className="text-caption leading-4 text-error">{retainError}</p> : null}
						</>
					) : null}

					{view === "result" && outcome ? (
						<>
							<p className="text-sm leading-5 text-settings-label" role="status">
								{t("settings.artifactStorage.freed", { size: formatBytes(outcome.freedBytes) })}
							</p>
							{failed > 0 ? (
								<p role="alert" className="text-caption leading-4 text-error">
									{t("settings.artifactStorage.notProcessed", { failed })}
								</p>
							) : null}
						</>
					) : null}
				</div>

				<div className={settingsDialogFooterClass}>
					{view === "list" ? (
						<>
							<Button type="button" variant="footer" disabled={eligible.length === 0} onClick={() => setSelected(new Set(eligible.map((s) => s.sessionId)))}>
								{t("settings.artifactStorage.selectEligible")}
							</Button>
							<Button type="button" variant="footer" disabled={chosen.length === 0} onClick={() => begin("delete")}>
								{t("settings.artifactStorage.delete")}
							</Button>
							<Button type="button" variant="footer-primary" disabled={chosen.length === 0} onClick={() => begin("archive")}>
								{t("settings.artifactStorage.archive")}
							</Button>
						</>
					) : null}
					{view === "confirm" ? (
						<>
							<Button type="button" variant="footer" disabled={saving} onClick={() => setView("list")}>
								{t("confirm.cancel")}
							</Button>
							<Button type="button" variant="footer-primary" disabled={saving} onClick={() => void run()}>
								{t(action === "archive" ? "settings.artifactStorage.archiveConfirm" : "settings.artifactStorage.deleteConfirm")}
							</Button>
						</>
					) : null}
					{view === "result" ? (
						<Button type="button" variant="footer-primary" onClick={onClose}>
							{t("common.close")}
						</Button>
					) : null}
				</div>
			</DialogContent>
		</Dialog>
	);
}
