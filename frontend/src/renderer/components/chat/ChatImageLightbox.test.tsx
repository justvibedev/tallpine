import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { tallpineBridge } from "../../lib/bridge";
import { ChatImage } from "./ChatImage";
import { ChatImageViewer } from "./ChatImageLightbox";

const png = new Uint8Array([137, 80, 78, 71]);
const urls = ["http://127.0.0.1:4000/a.png", "http://127.0.0.1:4000/b.png", "http://127.0.0.1:4000/c.png"];
const items = urls.map((url, i) => ({ url, src: url, label: `Shot ${i + 1}` }));

afterEach(() => vi.restoreAllMocks());

function viewer(index = 0) {
	return render(<ChatImageViewer items={items} index={index} open onOpenChange={() => {}} />);
}
const shown = () => screen.getByRole("img", { name: /Shot/ });
const zoomLabel = () => screen.getByRole("button", { name: "Reset zoom" }).textContent;

describe("ChatImageViewer", () => {
	it("pages with the buttons and arrow keys and stops at the ends", async () => {
		const user = userEvent.setup();
		viewer();
		expect(screen.getByText("1 of 3")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Previous image" })).toBeDisabled();
		await user.click(screen.getByRole("button", { name: "Next image" }));
		expect(shown()).toHaveAttribute("src", urls[1]);
		await user.keyboard("{ArrowRight}");
		expect(shown()).toHaveAttribute("src", urls[2]);
		expect(screen.getByRole("button", { name: "Next image" })).toBeDisabled();
		await user.keyboard("{ArrowRight}");
		expect(shown()).toHaveAttribute("src", urls[2]);
		await user.keyboard("{ArrowLeft}");
		expect(shown()).toHaveAttribute("src", urls[1]);
	});

	it("shows no paging controls for a single image", () => {
		render(<ChatImageViewer items={items.slice(0, 1)} open onOpenChange={() => {}} />);
		expect(screen.queryByRole("button", { name: "Next image" })).toBeNull();
	});

	it("zooms between 1x and 5x, resets, and resets when paging", async () => {
		const user = userEvent.setup();
		viewer();
		expect(zoomLabel()).toBe("100%");
		await user.keyboard("{+}{+}");
		expect(zoomLabel()).toBe("200%");
		for (let i = 0; i < 20; i++) await user.keyboard("{+}");
		expect(zoomLabel()).toBe("500%");
		expect(shown().style.transform).toContain("scale(5)");
		await user.keyboard("{-}");
		expect(zoomLabel()).toBe("450%");
		await user.keyboard("0");
		expect(zoomLabel()).toBe("100%");
		await user.keyboard("{+}");
		await user.click(screen.getByRole("button", { name: "Next image" }));
		expect(zoomLabel()).toBe("100%");
		fireEvent.wheel(screen.getByRole("img").parentElement!, { deltaY: -100 });
		expect(zoomLabel()).toBe("122%");
		fireEvent.wheel(screen.getByRole("img").parentElement!, { deltaY: -2 });
		expect(zoomLabel()).toBe("123%");
	});

	it("pans a zoomed image by dragging and not an unzoomed one", () => {
		viewer();
		const stage = shown().parentElement!;
		fireEvent.pointerDown(stage, { pointerId: 1, button: 0, clientX: 10, clientY: 10 });
		fireEvent.pointerMove(stage, { pointerId: 1, clientX: 40, clientY: 40 });
		expect(shown().style.transform).toContain("translate(0px, 0px)");
		fireEvent.keyDown(window, { key: "+" });
		fireEvent.pointerDown(stage, { pointerId: 1, button: 0, clientX: 10, clientY: 10 });
		fireEvent.pointerMove(stage, { pointerId: 1, clientX: 40, clientY: 25 });
		expect(shown().style.transform).toContain("translate(30px, 15px)");
		fireEvent.pointerUp(stage, { pointerId: 1 });
		fireEvent.pointerMove(stage, { pointerId: 1, clientX: 90, clientY: 90 });
		expect(shown().style.transform).toContain("translate(30px, 15px)");
	});

	it("copies with C and downloads with D", async () => {
		const user = userEvent.setup();
		const fetchImage = vi.spyOn(tallpineBridge.chatImage, "fetch").mockResolvedValue({ bytes: png, mimeType: "image/png" });
		const writeImage = vi.spyOn(tallpineBridge.clipboard, "writeImage").mockResolvedValue(undefined);
		const save = vi.spyOn(tallpineBridge.chatImage, "save").mockResolvedValue(true);
		viewer(1);
		await user.keyboard("c");
		await waitFor(() => expect(writeImage).toHaveBeenCalledExactlyOnceWith(png));
		expect(fetchImage).toHaveBeenCalledWith(urls[1]);
		await user.keyboard("d");
		await waitFor(() => expect(save).toHaveBeenCalledOnce());
		expect(save.mock.calls[0]![0]).toMatchObject({ name: "b.png" });
	});
});

describe("ChatImageViewer reopening", () => {
	it("reopens on the requested image at 100%", () => {
		const view = render(<ChatImageViewer items={items} index={2} open onOpenChange={() => {}} />);
		fireEvent.keyDown(window, { key: "+" });
		fireEvent.keyDown(window, { key: "ArrowLeft" });
		view.rerender(<ChatImageViewer items={items} index={2} open={false} onOpenChange={() => {}} />);
		view.rerender(<ChatImageViewer items={items} index={0} open onOpenChange={() => {}} />);
		expect(zoomLabel()).toBe("100%");
		expect(screen.getByText("1 of 3")).toBeInTheDocument();
		expect(shown()).toHaveAttribute("src", urls[0]);
	});
});

describe("ChatImage paging within a message", () => {
	it("opens on the clicked image and pages through the message's images only", async () => {
		const user = userEvent.setup();
		render(
			<>
				<div data-chat-image-scope="">
					<ChatImage src={urls[0]} alt="One" />
					<ChatImage src={urls[1]} alt="Two" />
					<ChatImage src={urls[2]} alt="Three" />
				</div>
				<ChatImage src="http://127.0.0.1:4000/other.png" alt="Elsewhere" />
			</>,
		);
		await user.click(screen.getByRole("button", { name: "Open image: Two" }));
		const dialog = screen.getByRole("dialog");
		expect(within(dialog).getByText("2 of 3")).toBeInTheDocument();
		expect(within(dialog).getByRole("img")).toHaveAttribute("src", urls[1]);
		await user.keyboard("{ArrowLeft}");
		expect(within(dialog).getByRole("img")).toHaveAttribute("src", urls[0]);
	});
});
