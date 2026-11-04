import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionContextMeter } from "./SessionContextMeter";
import type { ConversationUsage, TurnSettings } from "../../types/conversation";

const updateDefault = vi.fn().mockResolvedValue(undefined);
const restart = vi.fn();
const asked: (string | undefined)[] = [];
let harnessDefault: string | undefined = "70%";

vi.mock("../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { harnessDefaults: { "claude-code": { autoCompactWindow: harnessDefault } } } }),
	useUpdateHarnessDefault: () => ({ update: updateDefault, error: undefined }),
}));
vi.mock("../../hooks/useContextBreakdown", () => ({
	useContextBreakdown: (sessionId?: string) => { asked.push(sessionId); return { breakdown: undefined, loading: false }; },
}));
vi.mock("../../hooks/useModelUsage", () => ({ useModelUsage: () => ({ models: [] }) }));
vi.mock("../../hooks/useRestartAgent", () => ({
	useRestartAgent: () => ({ restart, restarting: false, busy: false, error: undefined }),
}));

const usage: ConversationUsage = {
	contextUsed: 100_000, contextWindow: 1_000_000, inputTokens: 1, outputTokens: 1, cachedTokens: 0, totalTokens: 2,
};

async function openPopover(settings: TurnSettings, onChooseSettings = vi.fn()) {
	render(<SessionContextMeter usage={usage} sessionId="s1" harness="claude-code" model="claude-sonnet-5-5" settings={settings} onChooseSettings={onChooseSettings} />);
	await userEvent.click(screen.getByRole("progressbar"));
	await screen.findByRole("dialog");
	return onChooseSettings;
}

describe("SessionContextMeter compaction control", () => {
	beforeEach(() => { updateDefault.mockClear(); restart.mockClear(); harnessDefault = "70%"; });

	it("changes this session's window to 40%, keeping its other choices and dropping the read-only flag", async () => {
		const onChoose = await openPopover({ model: "opus", reasoningEffort: "high", autoCompactPending: true });
		await userEvent.selectOptions(screen.getByRole("combobox"), "40%");
		expect(onChoose).toHaveBeenCalledWith({ model: "opus", reasoningEffort: "high", autoCompactWindow: "40%" });
	});

	it("shows applies-after-restart from the daemon's flag and restarts on click", async () => {
		await openPopover({ autoCompactWindow: "40%", autoCompactPending: true });
		expect(screen.getByRole("status")).toHaveTextContent("Applies after restart");
		await userEvent.click(screen.getByRole("button", { name: "Restart now (keeps history)" }));
		expect(restart).toHaveBeenCalledTimes(1);
	});

	it("shows applies-now when nothing is pending", async () => {
		await openPopover({ autoCompactWindow: "40%" });
		expect(screen.getByRole("status")).toHaveTextContent("Applies now");
	});

	it("saves the session's choice as the agent default", async () => {
		await openPopover({ autoCompactWindow: "40%" });
		await userEvent.click(screen.getByRole("button", { name: "Set as default" }));
		expect(updateDefault).toHaveBeenCalledWith({ agentId: "claude-code", autoCompactWindow: "40%" });
	});

	it("asks for the breakdown only for Claude Code, and only once the details open", async () => {
		asked.length = 0;
		render(<SessionContextMeter usage={usage} sessionId="s1" harness="claude-code" model="claude-sonnet-5-5" settings={{}} onChooseSettings={vi.fn()} />);
		expect(asked).toEqual([]);
		await userEvent.click(screen.getByRole("progressbar"));
		await screen.findByRole("dialog");
		expect(asked).toContain("s1");
	});

	it("never asks a Codex chat for a breakdown (totals only)", async () => {
		asked.length = 0;
		render(<SessionContextMeter usage={usage} sessionId="s1" harness="codex" model="gpt-5" settings={{}} onChooseSettings={vi.fn()} />);
		await userEvent.click(screen.getByRole("progressbar"));
		await screen.findByRole("dialog");
		expect(asked).toEqual([]);
	});

	it("has no control when the agent cannot take a window", async () => {
		render(<SessionContextMeter usage={usage} sessionId="s1" harness="codex" model="gpt-5" settings={{}} onChooseSettings={vi.fn()} />);
		await userEvent.click(screen.getByRole("progressbar"));
		await screen.findByRole("dialog");
		expect(screen.queryByRole("combobox")).toBeNull();
	});
});

describe("SessionContextMeter resolved compaction view", () => {
	beforeEach(() => { harnessDefault = undefined; });
	// Claude Code reports Tallpine's 400k compaction limit as its window; the popover
	// must show the daemon's capacity and threshold, not 40% of the reported 400k.
	const reported: ConversationUsage = { ...usage, contextUsed: 123_700, contextWindow: 400_000 };

	async function open(settings: TurnSettings) {
		render(<SessionContextMeter usage={reported} sessionId="s1" harness="claude-code" model="opus" settings={settings} onChooseSettings={vi.fn()} />);
		await userEvent.click(screen.getByRole("progressbar"));
		return screen.findByRole("dialog");
	}

	it("shows capacity 1M and the daemon's 400K threshold for 40%", async () => {
		const dialog = await open({ model: "opus", autoCompactWindow: "40%", contextCapacity: 1_000_000, compactThreshold: 400_000, modelLabel: "Opus 5.5" });
		expect(dialog.textContent).toContain("Opus 5.5");
		expect(dialog.textContent).toMatch(/Window.*1M/);
		expect(dialog.textContent).toMatch(/Compacts at.*400K \(40%\)/);
		expect(dialog.textContent).not.toContain("160K");
	});

	it("says a percent is not applied when the model window is unknown", async () => {
		const dialog = await open({ model: "custom", autoCompactWindow: "40%" });
		expect(dialog.textContent).toContain("40% not applied, model window unknown");
		// The reported 400K is Tallpine's own compaction limit, never shown as the window.
		expect(dialog.textContent).toMatch(/Window\s*unknown/);
		expect(dialog.textContent).not.toContain("400K");
	});

	it("does not send the daemon-set view back as a saved choice", async () => {
		const onChoose = vi.fn();
		render(<SessionContextMeter usage={reported} sessionId="s1" harness="claude-code" model="opus" settings={{ model: "opus", contextCapacity: 1_000_000, compactThreshold: 400_000, modelLabel: "Opus" }} onChooseSettings={onChoose} />);
		await userEvent.click(screen.getByRole("progressbar"));
		await screen.findByRole("dialog");
		await userEvent.selectOptions(screen.getByRole("combobox"), "40%");
		expect(onChoose).toHaveBeenCalledWith({ model: "opus", autoCompactWindow: "40%" });
	});
});

describe("SessionContextMeter model row", () => {
	it("shows the running model's picker name when the session saved no model", async () => {
		render(<SessionContextMeter usage={usage} sessionId="s1" harness="claude-code" model="opus" modelLabel="Opus 5.5" settings={{}} />);
		await userEvent.click(screen.getByRole("progressbar"));
		const dialog = await screen.findByRole("dialog");
		expect(dialog).toHaveTextContent("Opus 5.5");
		expect(dialog).not.toHaveTextContent("Unknown");
		expect(dialog).not.toHaveTextContent(/\bopus\b/);
	});
});
