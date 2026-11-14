import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { OwnerAsk } from "../../lib/ask-api";
import { renderWithClient } from "../../test/backlog-run-harness";
import { useUiStore } from "../../stores/ui-store";
import { AskCard } from "./AskCard";
import { AskInlineRow } from "./AskInlineRow";
import { AskStrip } from "./AskStrip";

const api = vi.hoisted(() => ({ listProjectAsks: vi.fn(), answerAsk: vi.fn() }));
vi.mock("../../lib/ask-api", async (importActual) => ({ ...(await importActual<typeof import("../../lib/ask-api")>()), ...api }));

export function askFixture(over: Partial<OwnerAsk> = {}): OwnerAsk {
	return {
		id: "a1", projectId: "p1", ref: "Q1", text: "Ship B62?", state: "open", anchored: true,
		options: [{ id: "y", label: "Yes, ship" }, { id: "n", label: "No, wait" }], recommended: "y",
		createdAt: "2026-11-13T10:00:00Z", updatedAt: "2026-11-13T10:00:00Z", ...over,
	};
}

beforeEach(() => {
	for (const fn of Object.values(api)) fn.mockReset();
	api.answerAsk.mockResolvedValue({ ask: askFixture({ state: "answered" }), deliveryFailed: false });
});

describe("AskCard option pills", () => {
	it("wrap long labels left-aligned inside a pill that grows with them", () => {
		const long = "Delete on server too: removes the ticket and its attachments for good (GDPR-friendly)";
		renderWithClient(<AskCard projectId="p1" ask={askFixture({ options: [{ id: "y", label: long }, { id: "n", label: "No" }], recommended: "y" })} />);
		const pill = screen.getByRole("button", { name: new RegExp(long.slice(0, 20)) });
		expect(pill.className).toContain("h-auto");
		expect(pill.className).toContain("text-left");
		expect(pill.className).not.toMatch(/(^| )h-7( |$)/);
	});
});

describe("AskStrip", () => {
	it("lists only this chat's open asks, in the main chat and in a lane", async () => {
		api.listProjectAsks.mockResolvedValue([
			askFixture({ id: "a1", ref: "Q1", text: "Main question" }),
			askFixture({ id: "a2", ref: "Q2", text: "Lane question", lane: "B7" }),
			askFixture({ id: "a3", ref: "Q3", text: "Already answered", state: "answered" }),
		]);
		const main = renderWithClient(<AskStrip projectId="p1" />);
		expect(await screen.findByText("Main question")).toBeInTheDocument();
		expect(screen.queryByText("Lane question")).not.toBeInTheDocument();
		expect(screen.queryByText("Already answered")).not.toBeInTheDocument();
		// The main chat points at the lane's ask instead of showing it.
		expect(screen.getByTestId("ask-lane-pointer")).toHaveTextContent("Q2 waits on you in lane B7.");
		main.unmount();
		renderWithClient(<AskStrip projectId="p1" lane="B7" />);
		expect(await screen.findByText("Lane question")).toBeInTheDocument();
		expect(screen.queryByText("Main question")).not.toBeInTheDocument();
		expect(screen.queryByTestId("ask-lane-pointer")).not.toBeInTheDocument();
	});

	it("renders nothing without open asks", async () => {
		api.listProjectAsks.mockResolvedValue([askFixture({ state: "answered" })]);
		const { container } = renderWithClient(<AskStrip projectId="p1" />);
		await waitFor(() => expect(api.listProjectAsks).toHaveBeenCalled());
		expect(container).toBeEmptyDOMElement();
	});

	it("collapses to a 'Waiting on you · N' header but never hides while asks are open", async () => {
		api.listProjectAsks.mockResolvedValue([askFixture({ id: "a1", ref: "Q1" }), askFixture({ id: "a2", ref: "Q2", text: "Second" })]);
		renderWithClient(<AskStrip projectId="p1" />);
		const header = await screen.findByTestId("ask-strip-header");
		expect(header).toHaveTextContent("Waiting on you · 2");
		await userEvent.click(header);
		expect(screen.queryByTestId("ask-card")).not.toBeInTheDocument();
		expect(screen.getByTestId("ask-strip-header")).toHaveTextContent("Waiting on you · 2");
		await userEvent.click(screen.getByTestId("ask-strip-header"));
		expect(screen.getAllByTestId("ask-card")).toHaveLength(2);
	});

	it("answers with one tap and refetches the asks", async () => {
		api.listProjectAsks.mockResolvedValue([askFixture()]);
		renderWithClient(<AskStrip projectId="p1" />);
		await userEvent.click(await screen.findByRole("button", { name: /No, wait/ }));
		await waitFor(() => expect(api.answerAsk).toHaveBeenCalledWith("p1", "Q1", { option: "n" }));
		await waitFor(() => expect(api.listProjectAsks).toHaveBeenCalledTimes(2));
	});

	it("tells the owner when the answer was saved but no orchestrator received it", async () => {
		api.answerAsk.mockResolvedValue({ ask: askFixture({ state: "answered" }), deliveryFailed: true });
		const toast = vi.spyOn(useUiStore.getState(), "showGlobalToast");
		api.listProjectAsks.mockResolvedValue([askFixture()]);
		renderWithClient(<AskStrip projectId="p1" />);
		await userEvent.click(await screen.findByRole("button", { name: /Yes, ship/ }));
		await waitFor(() => expect(toast).toHaveBeenCalledWith(expect.stringContaining("no orchestrator is running"), undefined, "error"));
	});

	it("sends free text from Other", async () => {
		api.listProjectAsks.mockResolvedValue([askFixture()]);
		renderWithClient(<AskStrip projectId="p1" />);
		await userEvent.type(await screen.findByRole("textbox", { name: "Another answer" }), "Only Friday");
		await userEvent.click(screen.getByRole("button", { name: "Send" }));
		await waitFor(() => expect(api.answerAsk).toHaveBeenCalledWith("p1", "Q1", { other: "Only Friday" }));
	});

	it("shows why an ask was repeated with the earlier answer", async () => {
		api.listProjectAsks.mockResolvedValue([
			askFixture({ id: "a0", ref: "Q1", state: "answered", answer: { optionId: "n", text: "No, wait" } }),
			askFixture({ id: "a1", ref: "Q2", reaskOf: "Q1", reaskReason: "scope changed" }),
		]);
		renderWithClient(<AskStrip projectId="p1" />);
		const reask = await screen.findByTestId("ask-reask");
		expect(reask).toHaveTextContent("Asked again: scope changed");
		expect(reask).toHaveTextContent("Earlier answer to Q1: No, wait");
	});

	it("offers the jump link only for an ask with an inline anchor", async () => {
		api.listProjectAsks.mockResolvedValue([askFixture({ id: "a1", ref: "Q1", anchored: true }), askFixture({ id: "a2", ref: "Q2", text: "No turn", anchored: false })]);
		renderWithClient(<AskStrip projectId="p1" />);
		await screen.findByText("No turn");
		expect(screen.getAllByTestId("ask-jump")).toHaveLength(1);
		expect(screen.getAllByTestId("ask-card")[0]).toContainElement(screen.getByTestId("ask-jump"));
	});
});

describe("AskCard states", () => {
	it("shows the answer once answered and the reason once withdrawn, with no buttons", () => {
		const a = renderWithClient(<AskCard projectId="p1" ask={askFixture({ state: "answered", answer: { text: "Yes, ship" } })} />);
		expect(screen.getByTestId("ask-answer")).toHaveTextContent("You answered: Yes, ship");
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
		a.unmount();
		renderWithClient(<AskCard projectId="p1" ask={askFixture({ state: "withdrawn", withdrawReason: "lane ended" })} />);
		expect(screen.getByTestId("ask-withdrawn")).toHaveTextContent("Withdrawn: lane ended");
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
	});

	it("marks the recommended option and surfaces a refused answer", async () => {
		api.answerAsk.mockRejectedValue(new Error("Q1 is answered: only an open ask can change"));
		renderWithClient(<AskCard projectId="p1" ask={askFixture()} />);
		expect(screen.getByRole("button", { name: /Yes, ship/ })).toHaveTextContent("recommended");
		await userEvent.click(screen.getByRole("button", { name: /Yes, ship/ }));
		expect(await screen.findByRole("alert")).toHaveTextContent("only an open ask can change");
	});
});

describe("AskInlineRow", () => {
	it("carries the ref the jump link scrolls to", () => {
		renderWithClient(<AskInlineRow ask={{ id: "a1", ref: "Q4" }} summary="Q4: Ship B62?" />);
		expect(screen.getByTestId("ask-inline")).toHaveAttribute("data-ask-ref", "Q4");
	});
});
