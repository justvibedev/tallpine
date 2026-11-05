import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ContextMeter } from "./ContextMeter";
import type { ConversationCompaction, ConversationRateLimits, ConversationUsage } from "../../types/conversation";

// The behaviour under test is the threshold encoding, because that is what carries
// the meaning: a user glances at the fill and the colour, and only reads the digits
// if something looks wrong. The numbers here are the real ones a live codex
// app-server reported (18055 of a 258400 window; a pro account at 71% of a 7 day
// rate limit window).

function usage(over: Partial<ConversationUsage> = {}): ConversationUsage {
	return {
		contextUsed: 18055,
		contextWindow: 258400,
		inputTokens: 18050,
		outputTokens: 5,
		cachedTokens: 11008,
		totalTokens: 18055,
		...over,
	};
}

function limits(over: Partial<ConversationRateLimits> = {}): ConversationRateLimits {
	return {
		primaryUsedPercent: 71,
		secondaryUsedPercent: -1,
		primaryResetsInSeconds: 490444,
		planLabel: "pro",
		...over,
	};
}

/** The circular fill inside the gauge. */
function fill(): SVGCircleElement {
	const inner = screen.getByRole("progressbar").querySelector("circle:last-child");
	if (!inner) throw new Error("meter has no fill element");
	return inner as SVGCircleElement;
}

describe("ContextMeter", () => {
	it("reads a percentage-only provider as a percentage, not tokens", async () => {
		render(<ContextMeter percentOnly usage={usage({ contextUsed: 3, contextWindow: 100 })} />);
		expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuetext", "3%");
		await userEvent.hover(screen.getByRole("progressbar"));
		const tooltip = await screen.findByRole("tooltip");
		expect(tooltip).toHaveTextContent("3% used");
		expect(tooltip).not.toHaveTextContent("tokens");
	});

	it("reports exact accessible values with compact numbers on hover", async () => {
		render(<ContextMeter usage={usage({ cost: 0.4594, currency: "USD" })} />);
		expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "7");
		expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuetext", "18,055 / 258,400 tokens (7%)");
		await userEvent.hover(screen.getByRole("progressbar"));
		const tooltip = await screen.findByRole("tooltip");
		expect(tooltip).toHaveTextContent("18.1K / 258.4K tokens (7%)");
		expect(tooltip).not.toHaveTextContent("tokens spent in total");
		expect(tooltip).not.toHaveTextContent("Provider-reported cost");
	});

	it("names the size the agent compacts at", async () => {
		render(<ContextMeter usage={usage({ contextWindow: 1_000_000 })} compactsAt="200k" />);
		await userEvent.hover(screen.getByRole("progressbar"));
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Compacts on its own at 200K");
	});

	it("encodes fullness in the circular stroke", () => {
		render(<ContextMeter usage={usage({ contextUsed: 129200 })} />);
		expect(fill()).toHaveAttribute("stroke-dasharray", "50 100");
	});

	it("keeps a nearly-empty conversation's fill visible", () => {
		render(<ContextMeter usage={usage({ contextUsed: 100 })} />);
		expect(fill()).toHaveAttribute("stroke-dasharray", "8 100");
	});

	describe("threshold colours", () => {
		it("uses the Tallpine logo accent below 70%", () => {
			render(<ContextMeter usage={usage({ contextUsed: 172_000 })} />);
			expect(screen.getByRole("progressbar")).toHaveClass("text-logo-accent");
		});

		it("shifts to the needs-you token from 70%", () => {
			// Exactly at the boundary, which must warn rather than stay normal.
			render(<ContextMeter usage={usage({ contextUsed: 180_880 })} />);
			expect(screen.getByRole("progressbar")).toHaveClass("text-status-needs-you");
		});

		it("shifts to the exited token from 90%, where the next turn is at risk", () => {
			render(<ContextMeter usage={usage({ contextUsed: 232_560 })} />);
			expect(screen.getByRole("progressbar")).toHaveClass("text-status-exited");
		});
	});

	it("clamps a provider that overreports past its own window", () => {
		render(<ContextMeter usage={usage({ contextUsed: 300_000 })} />);
		// Full, not overflowing the track and not claiming 116%.
		expect(fill()).toHaveAttribute("stroke-dasharray", "100 100");
		expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "100");
	});

	it("shows known context without claiming a window the provider did not report", async () => {
		render(<ContextMeter usage={usage({ contextWindow: 0, contextUsed: 18_055 })} />);
		// No window means no honest fullness. Drawing an empty bar would claim a
		// conversation is roomy when it might be nearly full.
		expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
		const readout = screen.getByRole("img", { name: "Context 18,055 / unknown" });
		await userEvent.hover(readout);
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Context 18.1K / unknown");
	});

	it("does not present cumulative spending as current context", () => {
		const { container } = render(<ContextMeter usage={usage({ contextWindow: 0, contextUsed: 0, totalTokens: 900 })} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("draws a failed turn with zero reported used as an empty 0% ring", () => {
		render(<ContextMeter usage={usage({ contextUsed: 0, contextWindow: 262_144 })} />);
		expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "0");
		// An empty track, not the sliver a nearly-empty conversation gets.
		expect(fill()).toHaveAttribute("stroke-dasharray", "0 100");
		expect(screen.queryByText("?")).not.toBeInTheDocument();
	});

	it("renders nothing before the provider has reported anything", () => {
		const { container } = render(<ContextMeter />);
		// Absent is distinct from zero: an empty meter for an unreported conversation
		// would be a claim Tallpine has not earned.
		expect(container).toBeEmptyDOMElement();
	});

	describe("quota warning", () => {
		it("stays hidden while the account has comfortable headroom", () => {
			render(<ContextMeter usage={usage()} rateLimits={limits({ primaryUsedPercent: 20 })} />);
			// A readout that is always present becomes furniture, and this one has to be
			// noticed on the day it matters.
			expect(screen.queryByText(/quota/)).not.toBeInTheDocument();
		});

		it("appears once the account is close to a wall", () => {
			render(<ContextMeter usage={usage()} rateLimits={limits({ primaryUsedPercent: 76 })} />);
			expect(screen.getByText("76% quota")).toBeInTheDocument();
		});

		it("escalates its colour past 90%, where turns start failing", () => {
			render(<ContextMeter usage={usage()} rateLimits={limits({ primaryUsedPercent: 94 })} />);
			expect(screen.getByText("94% quota").className).toContain("text-status-exited");
		});

		it("warns on the tighter of the two windows", () => {
			render(
				<ContextMeter
					usage={usage()}
					rateLimits={limits({ primaryUsedPercent: 30, secondaryUsedPercent: 88 })}
				/>,
			);
			// The tighter window is the one that will actually stop the next turn.
			expect(screen.getByText("88% quota")).toBeInTheDocument();
		});

		it("ignores a window the provider did not report", () => {
			render(
				<ContextMeter
					usage={usage()}
					rateLimits={limits({ primaryUsedPercent: -1, secondaryUsedPercent: -1 })}
				/>,
			);
			// Negative is the daemon's "not reported". Treating it as data would draw a
			// meter running backwards.
			expect(screen.queryByText(/quota/)).not.toBeInTheDocument();
		});

		it("warns on quota even when no usage has been reported yet", () => {
			// The startup rate-limit read lands before the first turn, so this is the
			// state a user sees when opening a conversation on a nearly-spent account:
			// exactly when the warning is most useful.
			render(<ContextMeter rateLimits={limits({ primaryUsedPercent: 97 })} />);
			expect(screen.getByText("97% quota")).toBeInTheDocument();
			expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
		});
	});
});

describe("ContextMeter details popover", () => {
	const details = { harness: "claude-code", model: "claude-sonnet-5-5", autoCompact: "70%", capacity: 200_000, compactThreshold: 140_000, compactedAt: "2026-10-28T10:00:00Z" };

	it("opens on click with model, window, usage, threshold, tokens left and last compaction", async () => {
		render(<ContextMeter usage={usage({ contextUsed: 100_000, contextWindow: 200_000 })} details={details} />);
		await userEvent.click(screen.getByRole("progressbar"));
		const popover = await screen.findByRole("dialog");
		expect(popover).toHaveTextContent("claude-sonnet-5-5");
		expect(popover).toHaveTextContent("200K tokens");
		expect(popover).toHaveTextContent("100K tokens (50%)");
		expect(popover).toHaveTextContent("140K (70%)");
		expect(popover).toHaveTextContent("40K tokens");
		expect(popover).toHaveTextContent(new Date("2026-10-28T10:00:00Z").toLocaleString());
	});

	it("highlights on hover and focus and reads as pressable when it opens details", () => {
		const { rerender } = render(<ContextMeter usage={usage()} details={details} />);
		const circle = screen.getByRole("progressbar");
		expect(circle).toHaveClass("cursor-pointer", "hover:bg-interactive-active", "focus-visible:bg-interactive-active");
		// Without details there is nothing to open, so no pressable affordance.
		rerender(<ContextMeter usage={usage()} />);
		expect(screen.getByRole("progressbar")).not.toHaveClass("cursor-pointer");
		expect(screen.getByRole("progressbar").className).not.toContain("hover:bg-interactive-active");
	});

	it("opens from the keyboard", async () => {
		render(<ContextMeter usage={usage()} details={details} />);
		screen.getByRole("progressbar").focus();
		await userEvent.keyboard("{Enter}");
		expect(await screen.findByRole("dialog")).toBeInTheDocument();
	});

	it("says unknown instead of guessing a missing model or window", async () => {
		render(<ContextMeter usage={usage({ contextWindow: 0 })} details={{ harness: "claude-code", autoCompact: "70%" }} />);
		await userEvent.click(screen.getByRole("img"));
		const popover = await screen.findByRole("dialog");
		expect(popover).toHaveTextContent(/Model\s*unknown/);
		expect(popover).toHaveTextContent(/Window\s*unknown/);
		expect(popover).toHaveTextContent("70% not applied, model window unknown");
		expect(popover).toHaveTextContent(/Last compacted\s*Not yet/);
	});

	it("shows not supported for an agent that cannot be told where to compact", async () => {
		render(<ContextMeter usage={usage()} details={{ harness: "codex", model: "gpt-5", autoCompactUnsupported: true }} />);
		await userEvent.click(screen.getByRole("progressbar"));
		expect(await screen.findByRole("dialog")).toHaveTextContent("Not supported for this agent");
	});

	it("includes the account quota the meter has", async () => {
		render(<ContextMeter usage={usage()} details={{ ...details, rateLimits: limits() }} />);
		await userEvent.click(screen.getByRole("progressbar"));
		expect(await screen.findByRole("dialog")).toHaveTextContent("71% used, resets in 5d");
	});

	it("keeps the hover tooltip short and points at the details", async () => {
		render(<ContextMeter usage={usage()} details={details} compactsAt="70%" />);
		await userEvent.hover(screen.getByRole("progressbar"));
		const tooltip = await screen.findByRole("tooltip");
		expect(tooltip).toHaveTextContent("Click for details");
		expect(tooltip).not.toHaveTextContent("Last compacted");
	});
});

describe("ContextMeter details: a window the project sets", () => {
	it("says Auto follows the project's own window instead of the agent default", async () => {
		render(<ContextMeter usage={usage()} details={{ harness: "claude-code", projectTokens: 250_000 }} />);
		await userEvent.click(screen.getByRole("progressbar"));
		expect(await screen.findByRole("dialog")).toHaveTextContent("250K (set by project)");
	});
	it("a chosen window wins over the project's", async () => {
		render(<ContextMeter usage={usage({ contextWindow: 1_000_000 })} details={{ harness: "claude-code", autoCompact: "40%", capacity: 1_000_000, compactThreshold: 400_000, projectTokens: 250_000 }} />);
		await userEvent.click(screen.getByRole("progressbar"));
		const dialog = await screen.findByRole("dialog");
		expect(dialog).toHaveTextContent("400K (40%)");
		expect(dialog).not.toHaveTextContent("set by project");
	});

	describe("compaction lifecycle", () => {
		const details = { harness: "claude-code", model: "claude-sonnet-5-5", autoCompact: "70%", capacity: 1_000_000, compactThreshold: 700_000 };
		const open = async (lastCompaction?: ConversationCompaction) => {
			render(<ContextMeter usage={usage({ contextUsed: 62_000, contextWindow: 1_000_000 })} details={{ ...details, lastCompaction }} />);
			await userEvent.click(screen.getByRole("progressbar"));
			return screen.findByRole("dialog");
		};
		const at = "2026-10-28T12:04:00Z";
		const clock = new Date(at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

		it("shows when it compacted, the sizes before and after, and that it was automatic", async () => {
			const dialog = await open({ status: "completed", trigger: "auto", preTokens: 410_000, postTokens: 62_000, at });
			expect(dialog).toHaveTextContent(`Compacted ${clock}, 410K → 62K, auto`);
		});

		it("says manual when the user compacted", async () => {
			const dialog = await open({ status: "completed", trigger: "manual", preTokens: 410_000, postTokens: 62_000, at });
			expect(dialog).toHaveTextContent(`Compacted ${clock}, 410K → 62K, manual`);
			expect(dialog).not.toHaveTextContent(", auto");
		});

		it("shows a compacting state while it runs and a failure when it fails", async () => {
			expect(await open({ status: "compacting", at })).toHaveTextContent("Compacting…");
		});

		it("shows a failed compaction", async () => {
			expect(await open({ status: "failed", at })).toHaveTextContent("Compaction failed");
		});

		it("omits sizes the provider did not report", async () => {
			const dialog = await open({ status: "completed", at });
			expect(dialog).toHaveTextContent(`Compacted ${clock}`);
			expect(dialog).not.toHaveTextContent("→");
		});

		it("shows no compaction row when none was reported", async () => {
			const dialog = await open(undefined);
			expect(dialog).not.toHaveTextContent("Compaction");
			expect(dialog).not.toHaveTextContent("Compacting");
		});
	});
});
