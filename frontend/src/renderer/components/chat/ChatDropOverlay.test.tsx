import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ChatDropOverlay } from "./ChatDropOverlay";
import { withResizeNotes } from "./composer/composer-model";

const filesDrag = { types: ["Files"], files: [new File(["x"], "a.png", { type: "image/png" })] };

function setup(onFiles = vi.fn(() => true), enabled = true) {
	render(
		<ChatDropOverlay onFiles={onFiles} enabled={enabled}>
			<p>pane child</p>
		</ChatDropOverlay>,
	);
	return { onFiles, child: screen.getByText("pane child") };
}

describe("ChatDropOverlay", () => {
	it("shows the overlay when files are dragged over any part of the pane", () => {
		const { child } = setup();
		fireEvent.dragEnter(child, { dataTransfer: filesDrag });
		expect(screen.getByTestId("chat-drop-overlay")).toBeTruthy();
		expect(screen.getByText("Drop to attach")).toBeTruthy();
	});
	it("ignores text drags", () => {
		const { child } = setup();
		fireEvent.dragEnter(child, { dataTransfer: { types: ["text/plain"], files: [] } });
		expect(screen.queryByTestId("chat-drop-overlay")).toBeNull();
	});
	it("Escape cancels the overlay and the drop is then not attached", () => {
		const { child, onFiles } = setup();
		fireEvent.dragEnter(child, { dataTransfer: filesDrag });
		fireEvent.keyDown(window, { key: "Escape" });
		expect(screen.queryByTestId("chat-drop-overlay")).toBeNull();
		fireEvent.drop(child, { dataTransfer: filesDrag });
		expect(onFiles).not.toHaveBeenCalled();
	});
	it("attaches dropped files and hides the overlay", () => {
		const { child, onFiles } = setup();
		fireEvent.dragEnter(child, { dataTransfer: filesDrag });
		fireEvent.drop(child, { dataTransfer: filesDrag });
		expect(onFiles).toHaveBeenCalledWith(filesDrag.files);
		expect(screen.queryByTestId("chat-drop-overlay")).toBeNull();
	});
	it("hides when the drag leaves the pane", () => {
		const { child } = setup();
		fireEvent.dragEnter(child, { dataTransfer: filesDrag });
		fireEvent.dragLeave(child, { dataTransfer: filesDrag });
		expect(screen.queryByTestId("chat-drop-overlay")).toBeNull();
	});
	it("lets folder drags bubble to the app-wide folder drop", () => {
		const onWindowDrop = vi.fn();
		window.addEventListener("drop", onWindowDrop);
		const { child, onFiles } = setup();
		const folder = { ...filesDrag, items: [{ webkitGetAsEntry: () => ({ isDirectory: true }) }] };
		fireEvent.dragEnter(child, { dataTransfer: folder });
		expect(screen.queryByTestId("chat-drop-overlay")).toBeNull();
		fireEvent.drop(child, { dataTransfer: folder });
		expect(onFiles).not.toHaveBeenCalled();
		expect(onWindowDrop).toHaveBeenCalledTimes(1);
		window.removeEventListener("drop", onWindowDrop);
	});
	it("does nothing while disabled", () => {
		const { child, onFiles } = setup(vi.fn(() => true), false);
		fireEvent.dragEnter(child, { dataTransfer: filesDrag });
		fireEvent.drop(child, { dataTransfer: filesDrag });
		expect(screen.queryByTestId("chat-drop-overlay")).toBeNull();
		expect(onFiles).not.toHaveBeenCalled();
	});
});

describe("withResizeNotes", () => {
	it("links the original of a downscaled image", () => {
		const out = withResizeNotes("hi", [{ stagedPath: "a/1.jpg", resize: { note: "resized from 4000×3000 to 1568×1176", originalBytes: 9, originalPath: "a/0.png" } }]);
		expect(out).toContain("- a/1.jpg was resized from 4000×3000 to 1568×1176; full original: a/0.png");
	});
	it("leaves messages without resized images unchanged", () => {
		expect(withResizeNotes("hi", [{ stagedPath: "a/1.png" }])).toBe("hi");
	});
});
