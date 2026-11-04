import { FolderPlus } from "lucide-react";
import { useTranslation } from "react-i18next";

export function FolderDropOverlay({ active }: { active: boolean }) {
	const { t } = useTranslation();
	return active ? (
					<div
						aria-hidden="true"
						className="dialog-overlay pointer-events-none flex items-center justify-center"
						data-testid="folder-drop-overlay"
					>
						<div className="relative isolate flex w-[min(420px,calc(100vw-48px))] flex-col items-center gap-3 rounded-welcome-panel border border-dashed border-[var(--color-border-import-modal)] bg-[var(--color-bg-import-modal)] p-8 text-center shadow-[var(--shadow-import-modal)]">
							<span className="grid size-11 place-items-center rounded-xl bg-[var(--color-bg-import-chip)] text-[var(--color-text-import-muted)]">
								<FolderPlus className="size-5" aria-hidden="true" />
							</span>
							<p className="text-[15px] font-semibold text-[var(--color-text-import-title)]">
								{t("createProject.dropToAdd")}
							</p>
						</div>
					</div>
	) : null;
}
