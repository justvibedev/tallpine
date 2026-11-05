import { type WorkspaceSummary } from "../../types/workspace";
import { useSidebarOrderStore } from "../../stores/sidebar-order-store";

/** Pins are a local list preference; remote host rows and the Scratchpad have their own sections and are not pinnable. */
export function useProjectPin(workspace: WorkspaceSummary, isStandalone: boolean) {
	const canPin = !isStandalone && !workspace.hostId;
	const isPinned = useSidebarOrderStore((state) => canPin && state.pinnedProjectIds.includes(workspace.id));
	const setProjectPinned = useSidebarOrderStore((state) => state.setProjectPinned);
	return { canPin, isPinned, setPinned: (pinned: boolean) => setProjectPinned(workspace.id, pinned) };
}
