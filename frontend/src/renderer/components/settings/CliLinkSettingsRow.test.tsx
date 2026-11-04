import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { tallpineBridge } from "../../lib/bridge";
import { CliLinkSettings } from "./CliLinkSettingsRow";

vi.mock("../../lib/bridge", () => ({
	tallpineBridge: {
		cliLink: {
			inspect: vi.fn(),
			link: vi.fn(),
			unlink: vi.fn(),
		},
	},
}));

const inspect = tallpineBridge.cliLink.inspect as ReturnType<typeof vi.fn>;
const link = tallpineBridge.cliLink.link as ReturnType<typeof vi.fn>;
const unlink = tallpineBridge.cliLink.unlink as ReturnType<typeof vi.fn>;

beforeEach(async () => {
	await appI18n.changeLanguage("en");
	inspect.mockReset();
	link.mockReset();
	unlink.mockReset();
	inspect.mockResolvedValue({
		ok: true,
		status: {
			linkPath: "/tmp/bin/tallpine",
			proposedTarget: "/Apps/Tallpine.app/Contents/Resources/daemon/vo",
			proposedVersion: "9.1.0",
			current: { kind: "stale", target: "/Apps/Old.app/Contents/Resources/daemon/vo" },
		},
	});
	link.mockResolvedValue({ ok: true, changed: true, linkPath: "/tmp/bin/tallpine", targetPath: "/Apps/Tallpine.app/Contents/Resources/daemon/vo", version: "9.1.0" });
	unlink.mockResolvedValue({ ok: true, changed: true, linkPath: "/tmp/bin/tallpine", targetPath: "", version: "" });
});

describe("CliLinkSettings", () => {
	it("inspects the command on mount with no developer flag", async () => {
		render(<CliLinkSettings />);
		expect(await screen.findByText("Global tallpine command")).toBeInTheDocument();
		expect(inspect).toHaveBeenCalledOnce();
	});

	it("shows the current and proposed targets and confirms before relinking", async () => {
		const user = userEvent.setup();
		render(<CliLinkSettings />);
		expect(await screen.findByText(/Current target: \/Apps\/Old.app/)).toBeInTheDocument();
		expect(screen.getByText(/Proposed target: \/Apps\/Tallpine.app/)).toBeInTheDocument();
		expect(screen.getByText(/9.1.0/)).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Relink" }));
		expect(link).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Confirm" }));
		expect(link).toHaveBeenCalledWith(true, "/Apps/Old.app/Contents/Resources/daemon/vo");
	});

	it("shows an inspect rejection instead of a permanent loading state", async () => {
		inspect.mockRejectedValue(new Error("marker access denied"));
		render(<CliLinkSettings />);
		expect(await screen.findByRole("alert")).toHaveTextContent("marker access denied");
		expect(screen.queryByText("Checking the global tallpine command.")).not.toBeInTheDocument();
	});

	it("requires confirmation before unlinking the target shown", async () => {
		const user = userEvent.setup();
		render(<CliLinkSettings />);
		await user.click(await screen.findByRole("button", { name: "Unlink" }));
		expect(unlink).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Confirm" }));
		expect(unlink).toHaveBeenCalledWith(true, "/Apps/Old.app/Contents/Resources/daemon/vo");
	});

	it("offers no replacement when the current command is not an Tallpine link", async () => {
		inspect.mockResolvedValue({
			ok: true,
			status: {
				linkPath: "/tmp/bin/tallpine",
				proposedTarget: "/Apps/Tallpine.app/Contents/Resources/daemon/vo",
				proposedVersion: "9.1.0",
				current: { kind: "foreign", target: null },
			},
		});
		render(<CliLinkSettings />);
		expect(await screen.findByText(/left unchanged/)).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Link" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Relink" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Unlink" })).not.toBeInTheDocument();
	});
});
