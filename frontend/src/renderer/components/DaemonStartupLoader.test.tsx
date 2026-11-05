import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DaemonStartupLoader } from "./DaemonStartupLoader";

vi.mock("../hooks/useSystemRequirementsGate", () => ({
	useSystemRequirementsGate: () => ({
		query: { isSuccess: true, refetch: vi.fn() },
		requirements: [
			{ id: "git", label: "git", satisfied: true, required: true, detail: "/usr/bin/git" },
			{ id: "tmux", label: "tmux", satisfied: true, required: true, detail: "/usr/bin/tmux" },
			{ id: "gh", label: "gh", satisfied: true, required: false, detail: "/usr/bin/gh" },
		],
		blocked: false,
		requirementsBlocked: false,
	}),
}));

describe("DaemonStartupLoader", () => {
	it("shows only the mascot and announces startup to assistive tech", () => {
		render(<DaemonStartupLoader />);

		const status = screen.getByRole("status", { name: "Tallpine is starting" });
		expect(status.textContent).toBe("");
		expect(status.querySelector(".tallpine-startup-logo")).not.toBeNull();
	});

	it("explains a refused start on the startup screen (B568)", () => {
		render(
			<DaemonStartupLoader
				status={{ state: "stopped", code: "datadir_locked", message: "The previous Tallpine daemon (pid 7) is still shutting down, so a new daemon could not start." }}
			/>,
		);

		const alert = screen.getByRole("alert");
		expect(alert).toHaveTextContent("The previous Tallpine daemon (pid 7) is still shutting down");
		expect(screen.getByRole("status", { name: "Tallpine is starting" })).not.toContainElement(alert);
		expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
	});

	it("stays bare while the daemon starts normally", () => {
		render(<DaemonStartupLoader status={{ state: "starting" }} />);

		expect(screen.queryByTestId("daemon-failure-banner")).toBeNull();
	});
});
