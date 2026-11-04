import AsyncStorage from "@react-native-async-storage/async-storage";
import { useSyncExternalStore } from "react";
import { laneSelectionKey, type LaneSelection } from "./laneSelection";

// The remembered lane per machine and project. Not a secret: AsyncStorage.
const STORAGE_KEY = "tallpine.laneSelection.v1";

let selection: LaneSelection = new Map();
const listeners = new Set<() => void>();
let loaded: Promise<void> | null = null;

function publish(next: LaneSelection) {
	selection = next;
	for (const listener of listeners) listener();
}

async function load(): Promise<void> {
	try {
		const raw = await AsyncStorage.getItem(STORAGE_KEY);
		const parsed = raw ? (JSON.parse(raw) as Record<string, unknown>) : {};
		const stored = new Map<string, string>();
		for (const [key, value] of Object.entries(parsed)) if (typeof value === "string") stored.set(key, value);
		// A pick made before the load finished wins over the stored one.
		publish(new Map([...stored, ...selection]));
	} catch {
		/* a lost selection only means the default lane shows again */
	}
}

function subscribe(listener: () => void) {
	loaded ??= load();
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}

export function useLaneSelection(): LaneSelection {
	return useSyncExternalStore(subscribe, () => selection, () => selection);
}

export function selectLane(hostId: string, projectId: string, orchestratorId: string): void {
	publish(new Map(selection).set(laneSelectionKey(hostId, projectId), orchestratorId));
	void AsyncStorage.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(selection))).catch(() => {});
}

/** The remembered orchestrator id for a project, for code outside React (spawn). */
export function selectedLaneId(hostId: string, projectId: string): string | undefined {
	return selection.get(laneSelectionKey(hostId, projectId));
}
