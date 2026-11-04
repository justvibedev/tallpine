import { KeyboardSensor, PointerSensor, useSensor, useSensors } from "@dnd-kit/core";
import { sortableKeyboardCoordinates } from "@dnd-kit/sortable";

// Reorder drags start from the row's primary click surface. The 4px activation
// distance keeps a plain navigation/disclosure click from starting a drag;
// nested action buttons remain outside that activator surface.
const REORDER_ACTIVATION_DISTANCE = 4;
// A transparent 1x1 image replaces the browser's default drag ghost, so the
// dragged row stays put at reduced opacity instead of trailing under the cursor.
export const EMPTY_DRAG_IMAGE = typeof Image === "undefined" ? null : new Image();
if (EMPTY_DRAG_IMAGE) EMPTY_DRAG_IMAGE.src = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7";

/** Stable drag-context id per project's session list. */
export const sessionDndId = (projectId: string) => `sidebar-sessions-${projectId}`;

// Module constants: useSensor memoizes on the options object, and new sensors
// change dnd-kit's context, which re-renders every sortable row in the list.
const POINTER_SENSOR_OPTIONS = { activationConstraint: { distance: REORDER_ACTIVATION_DISTANCE } };
// Space-only start so Enter keeps opening the row.
const KEYBOARD_SENSOR_OPTIONS = {
	coordinateGetter: sortableKeyboardCoordinates,
	keyboardCodes: { start: ["Space"], cancel: ["Escape"], end: ["Space"] },
};

export function useReorderSensors() {
	return useSensors(
		useSensor(PointerSensor, POINTER_SENSOR_OPTIONS),
		useSensor(KeyboardSensor, KEYBOARD_SENSOR_OPTIONS),
	);
}
