import { useNavigate, useParams } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAppShortcut } from "../../hooks/useAppShortcut";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { rememberCaptureProject, readCaptureProject } from "../../lib/backlog-capture-project";
import { useUiStore } from "../../stores/ui-store";
import { STANDALONE_PROJECT_KIND } from "../../types/workspace";
import { BacklogCapturePopover } from "./BacklogCapturePopover";

/**
 * App-level Add to backlog. Always mounted in the shell so Cmd+Shift+K and the
 * palette entries work on every route, over the terminal and the browser panel.
 */
export function GlobalBacklogCapture() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const params = useParams({ strict: false }) as { projectId?: string; hostId?: string };
	const request = useUiStore((state) => state.backlogCaptureRequest);
	const requestCapture = useUiStore((state) => state.requestBacklogCapture);
	const showToast = useUiStore((state) => state.showGlobalToast);
	const workspaces = useWorkspaceQuery().data ?? [];
	const projects = useMemo(() => workspaces.filter((workspace) => workspace.kind !== STANDALONE_PROJECT_KIND), [workspaces]);
	const [open, setOpen] = useState(false);
	// Mounted on first use and kept after: its mutation and query hooks stay out of every route until someone captures.
	const [mounted, setMounted] = useState(false);
	const [initialText, setInitialText] = useState("");
	const lastNonce = useRef(request?.nonce ?? 0);

	useAppShortcut("backlog-capture", () => requestCapture());
	useEffect(() => {
		if (!request || request.nonce === lastNonce.current) return;
		lastNonce.current = request.nonce;
		setInitialText(request.title);
		setMounted(true);
		setOpen(true);
	}, [request]);

	// The project in view, else the last one captured to, else the first.
	const known = (id: string | undefined) => (id && projects.some((project) => project.id === id) ? id : undefined);
	const defaultProjectId = known(params.hostId ? undefined : params.projectId) ?? known(readCaptureProject());

	if (!mounted) return null;
	return (
		<BacklogCapturePopover
			open={open}
			initialText={initialText}
			projects={projects}
			defaultProjectId={defaultProjectId}
			onOpenChange={setOpen}
			onAdded={(item) => {
				rememberCaptureProject(item.projectId);
				showToast(t("backlog.capture.added", { ref: item.ref, title: item.title }), undefined, {
					action: { label: t("backlog.capture.open"), onClick: () => void navigate({ to: "/backlog", search: { project: item.projectId, item: item.id } }) },
				});
			}}
		/>
	);
}
