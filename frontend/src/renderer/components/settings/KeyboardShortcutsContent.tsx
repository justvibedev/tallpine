import { Check,Keyboard,Pencil,Plus,RotateCcw,X } from "lucide-react";
import { useMemo,useState } from "react";
import { useTranslation } from "react-i18next";
import {
APP_SHORTCUTS,
effectiveShortcutBindings,
shortcutBindingLabel
} from "../../../shared/shortcuts";
import { isMacPlatform } from "../../lib/platform";
import { cn } from "../../lib/utils";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { Tooltip,TooltipContent,TooltipTrigger } from "../ui/tooltip";
import { SettingsRow } from "./SettingsRow";
import { SettingsSearch } from "./SettingsSearch";
import { SettingsSection } from "./SettingsSection";
import { shortcutCategoryLabel,shortcutLabel,useShortcutRecorder } from "./shortcuts/useShortcutRecorder";

export function KeyboardShortcutsContent({
	active,
	titleHidden,
	isMac = isMacPlatform(),
}: {
	active: boolean;
	titleHidden?: boolean;
	isMac?: boolean;
}) {
	const { t } = useTranslation();
	const [query, setQuery] = useState("");
	const [confirmResetAll, setConfirmResetAll] = useState(false);
	const [resettingAll, setResettingAll] = useState(false);
	const [resetAllError, setResetAllError] = useState<string | null>(null);
	const { overrides, recording, conflict, setConflict, toast, showToast, dismissToast, beginRecording, applyBinding, handleResetBinding, handleRemoveBinding, resetAll } = useShortcutRecorder({ active, isMac });

	const filteredShortcuts = useMemo(() => {
		const needle = query.trim().toLowerCase();
		if (!needle) return APP_SHORTCUTS;
		return APP_SHORTCUTS.filter((shortcut) => {
			const labels = effectiveShortcutBindings(shortcut.id, isMac, overrides)
				.map((candidate) => shortcutBindingLabel(candidate, isMac))
				.join(" ");
			return `${shortcutLabel(shortcut.id, t)} ${shortcutCategoryLabel(shortcut.category, t)} ${labels}`.toLowerCase().includes(needle);
		});
	}, [isMac, overrides, query, t]);

	return (
		<>
			<SettingsSection
				title={t("settings.keyboardShortcuts")}
				titleHidden={titleHidden}
				grouped
				toolbar={
					<SettingsSearch
						aria-label={t("shortcut.searchPlaceholder")}
						placeholder={t("shortcut.searchPlaceholder")}
						value={query}
						onChange={setQuery}
						trailing={
							<Button
								type="button"
								variant="ghost"
								size="sm"
								className="text-settings-muted hover:text-settings-label"
								disabled={Object.keys(overrides).length === 0}
								onClick={() => setConfirmResetAll(true)}
							>
								<RotateCcw aria-hidden="true" />
								{t("shortcut.resetAll")}
							</Button>
						}
					/>
				}
			>
				{filteredShortcuts.map((shortcut) => {
					const bindings = effectiveShortcutBindings(shortcut.id, isMac, overrides);
					const modified = Object.hasOwn(overrides, shortcut.id);
					const isRecording = recording?.id === shortcut.id;
					return (
						<SettingsRow
							className={cn(isRecording && "bg-settings-row-hover")}
							key={shortcut.id}
							label={
								<span className="flex items-center gap-2">
									{shortcutLabel(shortcut.id, t)}
									{modified ? (
										<span className="rounded-full bg-settings-menu-selected px-2 py-0.5 text-micro text-settings-muted">
											{t("shortcut.modified")}
										</span>
									) : null}
								</span>
							}
							description={shortcutCategoryLabel(shortcut.category, t)}
						>
							{shortcut.customizable === false ? (
								<span className="ml-auto text-xs text-settings-muted">{t("shortcut.fixedIndexed")}</span>
							) : isRecording ? (
								<div className="ml-auto flex min-w-52 items-center gap-2 rounded-md border border-(--color-settings-accent) px-3 py-2 text-caption text-settings-label">
									<Keyboard className="size-icon-base animate-pulse" aria-hidden="true" />
									{t("shortcut.pressRecording")}
								</div>
							) : (
								<div className="ml-auto flex flex-wrap items-center justify-end gap-1.5">
									{bindings.length === 0 ? (
										<span className="text-xs text-settings-muted">{t("shortcut.unassigned")}</span>
									) : (
										bindings.map((candidate, index) => (
											<span
												className="inline-flex items-center rounded-md bg-(--color-bg-settings-input)"
												key={`${candidate.key}-${index}`}
											>
												<kbd className="px-2 py-1.5 font-mono text-caption text-settings-label">
													{shortcutBindingLabel(candidate, isMac)}
												</kbd>
												<Tooltip>
													<TooltipTrigger asChild>
														<button
															type="button"
															className="mr-1 inline-flex size-5 items-center justify-center rounded text-settings-muted hover:bg-settings-menu-selected hover:text-settings-label"
															aria-label={t("shortcut.removeBindingAria", { binding: shortcutBindingLabel(candidate, isMac), name: shortcutLabel(shortcut.id, t) })}
															onClick={() => void handleRemoveBinding(shortcut.id, index)}
														>
															<X className="size-3" aria-hidden="true" />
														</button>
													</TooltipTrigger>
													<TooltipContent>{t("shortcut.remove")}</TooltipContent>
												</Tooltip>
											</span>
										))
									)}
									<Tooltip>
										<TooltipTrigger asChild>
											<button
												type="button"
												className="inline-flex size-8 items-center justify-center rounded-md text-settings-muted hover:bg-settings-menu-selected hover:text-settings-label"
												aria-label={t("shortcut.changeAria", { name: shortcutLabel(shortcut.id, t) })}
												onClick={() => void beginRecording({ id: shortcut.id, mode: "replace" })}
											>
												<Pencil className="size-icon-base" aria-hidden="true" />
											</button>
										</TooltipTrigger>
										<TooltipContent>{t("shortcut.change")}</TooltipContent>
									</Tooltip>
									{bindings.length < 2 ? (
										<Tooltip>
											<TooltipTrigger asChild>
												<button
													type="button"
													className="inline-flex size-8 items-center justify-center rounded-md text-settings-muted hover:bg-settings-menu-selected hover:text-settings-label"
													aria-label={t("shortcut.addAltAria", { name: shortcutLabel(shortcut.id, t) })}
													onClick={() => void beginRecording({ id: shortcut.id, mode: "add" })}
												>
													<Plus className="size-icon-base" aria-hidden="true" />
												</button>
											</TooltipTrigger>
											<TooltipContent>{t("shortcut.add")}</TooltipContent>
										</Tooltip>
									) : null}
									{modified ? (
										<Tooltip>
											<TooltipTrigger asChild>
												<button
													type="button"
													className="inline-flex size-8 items-center justify-center rounded-md text-settings-muted hover:bg-settings-menu-selected hover:text-settings-label"
													aria-label={t("shortcut.resetAria", { name: shortcutLabel(shortcut.id, t) })}
													onClick={() => void handleResetBinding(shortcut.id)}
												>
													<RotateCcw className="size-icon-base" aria-hidden="true" />
												</button>
											</TooltipTrigger>
											<TooltipContent>{t("shortcut.reset")}</TooltipContent>
										</Tooltip>
									) : null}
								</div>
							)}
						</SettingsRow>
					);
				})}
				{filteredShortcuts.length === 0 ? (
					<div className="settings-row-bar text-sm text-settings-muted">{t("shortcut.noMatching")}</div>
				) : null}
			</SettingsSection>

			{toast ? (
				<div
					className="pointer-events-auto sticky bottom-2 z-10 ml-auto flex w-[min(24rem,100%)] items-start gap-3 rounded-xl border border-(--color-border-settings-dialog) bg-settings-dialog px-4 py-3 shadow-[var(--shadow-settings-dialog)]"
					role="status"
					aria-live="polite"
					aria-atomic="true"
				>
					<Check className="mt-0.5 size-icon-base shrink-0 text-success" aria-hidden="true" />
					<div className="min-w-0 flex-1">
						<p className="text-sm font-medium text-settings-label">{toast.title}</p>
						{toast.body ? <p className="mt-0.5 text-caption text-settings-muted">{toast.body}</p> : null}
					</div>
					{toast.undo ? (
						<button
							type="button"
							className="text-caption font-medium text-settings-label hover:underline"
							onClick={() => void toast.undo?.()}
						>
							{t("shortcut.undo")}
						</button>
					) : null}
					<button
						type="button"
						className="text-settings-muted hover:text-settings-label"
						aria-label={t("shortcut.dismissNotification")}
						onClick={() => dismissToast()}
					>
						<X className="size-icon-base" aria-hidden="true" />
					</button>
				</div>
			) : null}

			<ConfirmDialog
				open={confirmResetAll}
				title={t("shortcut.resetAll")}
				description={t("shortcut.resetAllConfirm")}
				confirmLabel={t("shortcut.resetAll")}
				destructive
				busy={resettingAll}
				error={resetAllError}
				onOpenChange={(next) => {
					setConfirmResetAll(next);
					if (!next) setResetAllError(null);
				}}
				onConfirm={() => {
					if (resettingAll) return;
					setResettingAll(true);
					setResetAllError(null);
					resetAll()
						.then(() => {
							setConfirmResetAll(false);
							showToast({ title: t("shortcut.restoredDefaults") });
						})
						.catch(() => setResetAllError(t("shortcut.resetAllFailed")))
						.finally(() => setResettingAll(false));
				}}
			/>

			<ConfirmDialog
				open={conflict !== null}
				title={t("shortcut.alreadyInUse")}
				description={
					conflict
						? t("shortcut.conflictBody", {
								binding: shortcutBindingLabel(conflict.binding, isMac),
								from: shortcutLabel(conflict.conflictingId, t),
								to: shortcutLabel(conflict.targetId, t),
							})
						: ""
				}
				confirmLabel={t("shortcut.reassign")}
				onOpenChange={(next) => {
					if (!next) setConflict(null);
				}}
				onConfirm={() => {
					if (!conflict) return;
					const pending = conflict;
					setConflict(null);
					void applyBinding(
						pending.targetId,
						pending.binding,
						pending.mode,
						pending.conflictingId,
					).catch(() =>
						showToast({
							title: t("shortcut.couldNotReassign"),
							body: t("shortcut.previousBindingsActive"),
						}),
					);
				}}
			/>
		</>
	);
}
