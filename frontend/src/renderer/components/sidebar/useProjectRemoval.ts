import { useTranslation } from "react-i18next";
import { useState } from "react";
import { type WorkspaceSummary } from "../../types/workspace";
import { type Selection } from "./types";

export function useProjectRemoval({ workspace, selection, onRemoveProject }: { workspace: WorkspaceSummary; selection: Selection; onRemoveProject: (projectId: string) => Promise<void> }) {
	const { t } = useTranslation();
	const [removeError, setRemoveError] = useState<string | null>(null);
	const [isRemoving, setIsRemoving] = useState(false);
	const [confirmOpen, setConfirmOpen] = useState(false);

	const removeProject = () => {
		setRemoveError(null);
		setConfirmOpen(true);
	};

	const handleConfirmRemove = async () => {
		setConfirmOpen(false);
		setIsRemoving(true);
		// Teardown can take a while when a project owns several sessions. Leave
		// the confirmation immediately and move to the route that remains valid
		// after removal while the sidebar keeps progress/error feedback visible.
		if (selection.activeProjectId === workspace.id) selection.goHome();
		try {
			await onRemoveProject(workspace.id);
		} catch (err) {
			const message = err instanceof Error ? err.message : t("shell.couldNotRemoveProject");
			setRemoveError(message);
		} finally {
			setIsRemoving(false);
		}
	};

	return { removeError, isRemoving, confirmOpen, setConfirmOpen, removeProject, handleConfirmRemove };
}
