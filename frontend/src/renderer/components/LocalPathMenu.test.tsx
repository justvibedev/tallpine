import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { tallpineBridge } from "../lib/bridge";
import type { LocalPathInspection } from "../../shared/local-path";
import { LocalPathMenu } from "./LocalPathMenu";

function spies(inspection: LocalPathInspection) {
	const write = vi.spyOn(tallpineBridge.clipboard, "writeText").mockResolvedValue(undefined);
	const inspect = vi.spyOn(tallpineBridge.localPath, "inspect").mockResolvedValue(inspection);
	const perform = vi.spyOn(tallpineBridge.localPath, "perform").mockResolvedValue({ ok: true, performed: "open" });
	return { write, inspect, perform };
}

describe("LocalPathMenu", () => {
	afterEach(() => vi.restoreAllMocks());

	it("opens an existing file on plain click and reveals it on Cmd-click", async () => {
		const { write, perform } = spies({ ok: true, path: "/Users/me/report.pdf", kind: "file", canOpen: true });
		render(<LocalPathMenu path="/Users/me/report.pdf"><code>/Users/me/report.pdf</code></LocalPathMenu>);
		const target = screen.getByRole("button");

		fireEvent.click(target);
		await waitFor(() => expect(perform).toHaveBeenCalledExactlyOnceWith("/Users/me/report.pdf", "open"));
		expect(write).not.toHaveBeenCalled();

		fireEvent.click(target, { metaKey: true });
		expect(perform).toHaveBeenLastCalledWith("/Users/me/report.pdf", "reveal");
	});

	it("still copies a directory on plain click", async () => {
		const { write, perform } = spies({ ok: true, path: "/Users/me/.vo-local-ci/gocache", kind: "directory", canOpen: true });
		render(<LocalPathMenu path="~/.vo-local-ci/gocache"><code>~/.vo-local-ci/gocache</code></LocalPathMenu>);

		fireEvent.click(screen.getByRole("button"));
		await waitFor(() => expect(write).toHaveBeenCalledWith("~/.vo-local-ci/gocache"));
		expect(perform).not.toHaveBeenCalled();
	});

	it("copies a path that does not exist rather than toasting", async () => {
		const { write, perform } = spies({ ok: false, reason: "not_found" });
		render(<LocalPathMenu path="/a/b"><code>/a/b</code></LocalPathMenu>);

		fireEvent.click(screen.getByRole("button"));
		await waitFor(() => expect(write).toHaveBeenCalledWith("/a/b"));
		expect(perform).not.toHaveBeenCalled();
	});

	it("follows the same rule for the Enter key", async () => {
		const { write, perform } = spies({ ok: true, path: "/a/b.txt", kind: "file", canOpen: true });
		render(<LocalPathMenu path="/a/b.txt"><code>/a/b.txt</code></LocalPathMenu>);
		const target = screen.getByRole("button");

		fireEvent.keyDown(target, { key: "Enter" });
		await waitFor(() => expect(perform).toHaveBeenCalledWith("/a/b.txt", "open"));

		fireEvent.keyDown(target, { key: "Enter", metaKey: true });
		expect(perform).toHaveBeenLastCalledWith("/a/b.txt", "reveal");
		expect(write).not.toHaveBeenCalled();
	});
});
