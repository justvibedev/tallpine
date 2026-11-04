import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { tallpineBridge } from "../lib/bridge";
import { LocalPathMenu } from "./LocalPathMenu";

describe("LocalPathMenu", () => {
	afterEach(() => vi.restoreAllMocks());

	it("copies the path on plain click and reveals it on Cmd-click", () => {
		const write = vi.spyOn(tallpineBridge.clipboard, "writeText").mockResolvedValue(undefined);
		const perform = vi.spyOn(tallpineBridge.localPath, "perform").mockResolvedValue({ ok: true, performed: "reveal" });
		render(<LocalPathMenu path="~/.vo-local-ci/gocache"><code>~/.vo-local-ci/gocache</code></LocalPathMenu>);
		const target = screen.getByRole("button");

		fireEvent.click(target);
		expect(write).toHaveBeenCalledWith("~/.vo-local-ci/gocache");
		expect(perform).not.toHaveBeenCalled();

		fireEvent.click(target, { metaKey: true });
		expect(perform).toHaveBeenCalledWith("~/.vo-local-ci/gocache", "reveal");
	});

	it("follows the same rule for the Enter key", () => {
		const write = vi.spyOn(tallpineBridge.clipboard, "writeText").mockResolvedValue(undefined);
		const perform = vi.spyOn(tallpineBridge.localPath, "perform").mockResolvedValue({ ok: true, performed: "reveal" });
		render(<LocalPathMenu path="/a/b"><code>/a/b</code></LocalPathMenu>);
		const target = screen.getByRole("button");

		fireEvent.keyDown(target, { key: "Enter" });
		expect(write).toHaveBeenCalledWith("/a/b");
		expect(perform).not.toHaveBeenCalled();

		fireEvent.keyDown(target, { key: "Enter", metaKey: true });
		expect(perform).toHaveBeenCalledWith("/a/b", "reveal");
	});
});
