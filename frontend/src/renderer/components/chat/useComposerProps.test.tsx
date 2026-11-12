import { renderHook } from "@testing-library/react";
import type { ReactElement } from "react";
import { describe, expect, it, vi } from "vitest";
import { chatFixture } from "../../lib/chat-fixture";
import type { ConversationSnapshot } from "../../types/conversation";
import { useComposerProps } from "./useComposerProps";

vi.mock("./TurnSettingsBar", () => ({ TurnSettingsBar: () => null }));
vi.mock("./SessionContextMeter", () => ({ SessionContextMeter: () => null }));

type BarProps = { stopped: boolean; disabled: boolean };

function settingsBar(overrides: { state: "stopped" | "running"; agentResuming?: boolean; controllerTransitioning?: boolean; newWorkDisabled?: boolean; provisioning?: boolean }) {
	const snapshot = { ...chatFixture, controller: { ...chatFixture.controller, state: overrides.state } } as ConversationSnapshot;
	const { result } = renderHook(() =>
		useComposerProps({
			snapshot,
			settingsReady: false,
			onChooseSettings: vi.fn(),
			onChooseConfigOption: undefined,
			settingsLoading: false,
			newWorkDisabled: overrides.newWorkDisabled ?? false,
			agentResuming: overrides.agentResuming ?? false,
			controllerTransitioning: overrides.controllerTransitioning,
			session: overrides.provisioning ? ({ provisionState: "provisioning" } as never) : undefined,
			visibleQueuedMessages: [],
			localEchos: undefined,
			backgroundWakeQueuedTurnIds: new Set<string>(),
		} as unknown as Parameters<typeof useComposerProps>[0]),
	);
	const element = result.current.composerSettings as ReactElement<BarProps> | null;
	return element?.props;
}

describe("useComposerProps stopped gating", () => {
	it("keeps the picker usable on a stopped agent even before settings are ready", () => {
		const props = settingsBar({ state: "stopped" });
		expect(props?.stopped).toBe(true);
		expect(props?.disabled).toBe(false);
	});

	it.each([
		["resuming", { agentResuming: true }],
		["mid-handoff", { controllerTransitioning: true }],
		["new work disabled", { newWorkDisabled: true }],
		["provisioning", { provisioning: true }],
	])("does not treat a stopped agent as pickable while %s", (_name, extra) => {
		const props = settingsBar({ state: "stopped", ...extra });
		expect(props?.stopped ?? false).toBe(false);
	});

	it("hides the bar for a running agent whose settings are not ready", () => {
		expect(settingsBar({ state: "running" })).toBeUndefined();
	});
});
