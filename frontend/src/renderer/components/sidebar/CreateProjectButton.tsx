import { useTranslation } from "react-i18next";
import { Plus } from "lucide-react";
import { useState } from "react";
import { STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { useUiStore } from "../../stores/ui-store";
import { CreateProjectFlow } from "../CreateProjectFlow";
import { ROW_ACTION_BUTTON_CLASS } from "./constants";
import { type SidebarProps } from "./types";

export function CreateProjectButton({
	existingProjectPaths,
	remoteHosts,
	onCreateRemoteProject,
	onInitializeRemoteProject,
	onCloneProject,
	onCreateProject,
	onInitializeProject,
	onOpenExistingProject,
}: Pick<SidebarProps, "onCloneProject" | "onCreateProject" | "onInitializeProject" | "onCreateRemoteProject" | "onInitializeRemoteProject" | "remoteHosts"> & {
	existingProjectPaths: readonly string[];
	onOpenExistingProject: (path: string) => void | Promise<void>;
}) {
	const { t } = useTranslation();
	// Single CreateProjectFlow owner for the sidebar: the header "+" stays mounted
	// (CSS-hidden when collapsed) so it can own
	// openSignal for ⌘N on every shell route. The collapsed rail button below
	// reuses this flow via requestCreateProject().
	const createProjectNonce = useUiStore((state) => state.createProjectNonce);
	const folderDropRequest = useUiStore((state) => state.folderDropRequest);
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const [hostId, setHostId] = useState<string>();
	const host = remoteHosts?.find((candidate) => candidate.hostId === hostId);
	return (
		<CreateProjectFlow
			droppedPath={folderDropRequest}
			existingProjectPaths={existingProjectPaths}
			remoteHosts={remoteHosts}
			hostId={hostId}
			hostLabel={host?.label ?? hostId}
			connected={!hostId || host?.status === "connected"}
			onSelectHost={setHostId}
			onDismiss={() => setHostId(undefined)}
			mode="choose"
			onCloneProject={onCloneProject}
			onCreateProject={async (input) => {
				if (hostId) {
					await onCreateRemoteProject(hostId, input);
					setHostId(undefined);
				} else await onCreateProject(input);
			}}
			onCreateStandaloneAgent={() => requestNewTask(STANDALONE_WORKSPACE_ID, hostId)}
			onInitializeProject={(path) => hostId ? onInitializeRemoteProject(hostId, path) : onInitializeProject(path)}
			onOpenExistingProject={onOpenExistingProject}
			openSignal={createProjectNonce}
		>
			{({ disabled, choosePath, label }) => (
				<Tooltip>
					<TooltipTrigger asChild>
						<span className="inline-flex">
							<button
								aria-label={t("shell.newProject")}
								className={ROW_ACTION_BUTTON_CLASS}
								disabled={disabled}
								onClick={choosePath}
								type="button"
							>
									<Plus aria-hidden="true" />
							</button>
						</span>
					</TooltipTrigger>
					<TooltipContent>{label}</TooltipContent>
				</Tooltip>
			)}
		</CreateProjectFlow>
	);
}
