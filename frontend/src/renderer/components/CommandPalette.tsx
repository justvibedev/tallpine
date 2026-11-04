import { memo } from "react";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { useUiStore } from "../stores/ui-store";
import { CreateProjectFlow } from "./CreateProjectFlow";

import { CommandPaletteDialog } from "./command-palette/CommandPaletteDialog";
import { usePaletteData } from "./command-palette/usePaletteData";
import { usePaletteNavigation } from "./command-palette/usePaletteNavigation";
import { usePaletteActions } from "./command-palette/usePaletteActions";


// Memoized (it takes no props) so the shell's re-render on every workspace write
// does not re-render, and rebuild every command of, a palette that is closed.
export const CommandPalette = memo(function CommandPalette() {
	const data = usePaletteData();
	const nav = usePaletteNavigation(data);
	const actions = usePaletteActions(data, nav);
	const {
		cloneProject,
		createProject,
		createProjectFlowMounted,
		createProjectFlowOpenSignal,
		enabled,
		initializeProjectRepository,
		workspaces,
	} = data;
	const {
		openExistingProject,
	} = nav;
	if (!enabled) return null;
	const v = { ...data, ...nav, ...actions };

	return (
		<>
			<CommandPaletteDialog v={v} />

			{createProjectFlowMounted ? (
				<CreateProjectFlow
					mode="choose"
					openSignal={createProjectFlowOpenSignal}
					onCloneProject={cloneProject}
					onCreateProject={createProject}
					onCreateStandaloneAgent={() => useUiStore.getState().requestNewTask(STANDALONE_WORKSPACE_ID)}
					onInitializeProject={initializeProjectRepository}
					onOpenExistingProject={openExistingProject}
					existingProjectPaths={workspaces.map((workspace) => workspace.path)}
					existingProjectNames={workspaces.map((workspace) => workspace.name)}
				/>
			) : null}
		</>
	);
});
