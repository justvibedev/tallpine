import { useSettings } from "./useSettings";

/** Whether the daemon has backlog run mode on; false while settings load, so nothing flashes in. */
export function useBacklogRunEnabled(): boolean {
	return useSettings().settings?.backlogRunEnabled === true;
}
