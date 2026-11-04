import { beforeEach, describe, expect, it } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { orchestratorInspectorView } from "./orchestrator-inspector-view";

describe("orchestrator inspector default tab", () => {
	beforeEach(() => {
		window.localStorage.clear();
		useUiStore.setState({ inspectorSessions: {} });
	});

	it("opens a fresh orchestrator on Plan, not Browser", () => {
		useUiStore.getState().initializeInspectorSession("o1", false, true);
		const stored = useUiStore.getState().inspectorSessions.o1?.view;
		expect(orchestratorInspectorView(stored, true)).toBe("plan");
	});

	it("falls back to Browser only when it is the sole tab", () => {
		expect(orchestratorInspectorView(undefined, false)).toBe("browser");
	});

	it("restores the remembered tab after a rebuild", () => {
		useUiStore.getState().setInspectorView("o1", "browser", true);
		useUiStore.setState({ inspectorSessions: {} });
		useUiStore.getState().initializeInspectorSession("o1", false, true);
		expect(orchestratorInspectorView(useUiStore.getState().inspectorSessions.o1?.view, true)).toBe("browser");
	});

	it("still opens Browser when explicitly targeted", () => {
		expect(orchestratorInspectorView("browser", true)).toBe("browser");
	});
});
