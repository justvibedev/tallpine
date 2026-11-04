import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { tallpineBridge } from "../lib/bridge";
import { RemoteImg } from "./RemoteImg";

afterEach(() => vi.restoreAllMocks());

describe("RemoteImg", () => {
	it("loads a remote avatar through the main process, never with the remote URL as src", async () => {
		const fetchImage = vi.spyOn(tallpineBridge.chatImage, "fetch").mockResolvedValue({ bytes: new Uint8Array([1, 2, 3]), mimeType: "image/png" });
		render(<RemoteImg alt="avatar" src="https://avatars.example.com/u/1" />);
		const image = await screen.findByAltText("avatar");
		expect(fetchImage).toHaveBeenCalledWith("https://avatars.example.com/u/1");
		expect(image.getAttribute("src")).toMatch(/^data:image\/png;base64,/);
	});

	it("passes loopback sources straight through without a fetch", () => {
		const fetchImage = vi.spyOn(tallpineBridge.chatImage, "fetch");
		render(<RemoteImg alt="local" src="http://127.0.0.1:3101/x.png" />);
		expect(screen.getByAltText("local")).toHaveAttribute("src", "http://127.0.0.1:3101/x.png");
		expect(fetchImage).not.toHaveBeenCalled();
	});
});
