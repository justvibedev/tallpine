import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChatLinkProvider, ChatMarkdown, ChatOpeningRef } from "./ChatMarkdown";

describe("ChatMarkdown streaming fade", () => {
	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
	});
	const fading = (container: HTMLElement) => container.querySelectorAll(".stream-char").length;

	it("never wraps a reply that mounts settled", () => {
		const { container } = render(<ChatMarkdown text="from history" />);
		expect(fading(container)).toBe(0);
	});

	it("does not fade text already on screen when a chat opens mid-stream", () => {
		const opening = { current: true };
		const reply = (text: string) => (
			<ChatOpeningRef.Provider value={opening}>
				<ChatMarkdown text={text} streaming />
			</ChatOpeningRef.Provider>
		);
		const { container, rerender } = render(reply("already shown"));
		opening.current = false;
		expect(fading(container)).toBe(0);
		expect(container.textContent).toBe("already shown");

		rerender(reply("already shown and more"));
		expect(fading(container)).toBe(" and more".length);
		expect(container.textContent).toBe("already shown and more");
	});

	it("bounds the fade spans to the newest characters of a long reply", () => {
		const head = "alpha ".repeat(300);
		const { container, rerender } = render(<ChatMarkdown text={head} streaming />);
		rerender(<ChatMarkdown text={`${head}beta`} streaming />);
		expect(fading(container)).toBeLessThanOrEqual(600);
		expect(fading(container)).toBeGreaterThan(0);
		expect(container.textContent).toBe(`${head}beta`);
		// The newest characters are the ones that fade.
		const spans = [...container.querySelectorAll(".stream-char")].map((node) => node.textContent).join("");
		expect(spans.endsWith("beta")).toBe(true);
	});

	it("fades while streaming and sheds the spans shortly after it settles", () => {
		vi.useFakeTimers();
		const { container, rerender } = render(<ChatMarkdown text="live text" streaming />);
		expect(fading(container)).toBeGreaterThan(0);
		expect(container.textContent).toBe("live text");

		rerender(<ChatMarkdown text="live text" />);
		expect(fading(container)).toBeGreaterThan(0);
		act(() => vi.advanceTimersByTime(500));
		expect(fading(container)).toBe(0);
		expect(container.textContent).toBe("live text");
	});

	it("keeps fading across a retry that restarts the stream inside the grace period", () => {
		vi.useFakeTimers();
		const { container, rerender } = render(<ChatMarkdown text="one" streaming />);
		rerender(<ChatMarkdown text="one" />);
		act(() => vi.advanceTimersByTime(200));
		rerender(<ChatMarkdown text="one two" streaming />);
		act(() => vi.advanceTimersByTime(500));
		expect(fading(container)).toBeGreaterThan(0);
	});

	it("renders plain text under reduced motion", () => {
		vi.spyOn(window, "matchMedia").mockImplementation((query) => ({
			matches: query === "(prefers-reduced-motion: reduce)",
			media: query,
			onchange: null,
			addEventListener: vi.fn(),
			removeEventListener: vi.fn(),
			addListener: vi.fn(),
			removeListener: vi.fn(),
			dispatchEvent: vi.fn(() => false),
		}));
		const { container } = render(<ChatMarkdown text="no motion" streaming />);
		expect(fading(container)).toBe(0);
		expect(screen.getByText("no motion")).toBeInTheDocument();
	});

	it("still compacts emoji while the fade is on", () => {
		const { container } = render(<ChatMarkdown text="done ✅ now" streaming />);
		expect(container.querySelector(".chat-md-emoji")).toHaveTextContent("✅");
		expect(container.textContent).toBe("done ✅ now");
	});

	it("keeps links working while their text fades", () => {
		const onLinkOpen = vi.fn();
		render(
			<ChatLinkProvider onLinkOpen={onLinkOpen}>
				<ChatMarkdown text="see [docs](https://example.com/docs)" streaming />
			</ChatLinkProvider>,
		);
		expect(screen.getByRole("link")).toHaveTextContent("docs");
	});
});
