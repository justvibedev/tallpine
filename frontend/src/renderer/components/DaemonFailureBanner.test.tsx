import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { tallpineBridge } from "../lib/bridge";
import { appI18n } from "../i18n";
import { DaemonFailureBanner } from "./DaemonFailureBanner";

describe("DaemonFailureBanner", () => {
	afterEach(async () => {
		vi.useRealTimers();
		vi.restoreAllMocks();
		await appI18n.changeLanguage("en");
	});

	it("shows the daemon failure message, code, and actionable hint", () => {
		render(
			<DaemonFailureBanner
				status={{
					state: "stopped",
					code: "exited",
					message: "Tallpine daemon exited with code 1",
					details: "go: go.mod requires go >= 1.25.7",
				}}
			/>,
		);

		expect(screen.getByRole("alert")).toHaveTextContent("Tallpine daemon failed to start");
		expect(screen.getByRole("alert")).toHaveTextContent("Tallpine daemon exited with code 1");
		expect(screen.getByText("exited")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Restart daemon" })).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Show details" }));
		expect(screen.getByText("go: go.mod requires go >= 1.25.7")).toBeInTheDocument();
	});

	it("resets copy feedback when failure details change", async () => {
		const { rerender } = render(
			<DaemonFailureBanner status={{ state: "stopped", code: "exited", details: "first failure" }} />,
		);

		await act(async () => {
			fireEvent.click(screen.getByRole("button", { name: "Copy details" }));
		});
		expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();

		rerender(<DaemonFailureBanner status={{ state: "stopped", code: "exited", details: "second failure" }} />);

		expect(screen.getByRole("button", { name: "Copy details" })).toBeInTheDocument();
	});

	it("resets copy feedback after two seconds", async () => {
		vi.useFakeTimers();
		render(<DaemonFailureBanner status={{ state: "stopped", code: "exited", details: "failure" }} />);

		await act(async () => {
			fireEvent.click(screen.getByRole("button", { name: "Copy details" }));
		});
		expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();

		act(() => vi.advanceTimersByTime(2_000));

		expect(screen.getByRole("button", { name: "Copy details" })).toBeInTheDocument();
	});

	it("renders nothing while the daemon is not in an error state", () => {
		const { container } = render(<DaemonFailureBanner status={{ state: "starting" }} />);

		expect(container).toBeEmptyDOMElement();
	});

	it("shows slow startup as progress without offering a restart", () => {
		render(
			<DaemonFailureBanner
				status={{
					state: "starting",
					message: "Tallpine daemon is still starting. Session recovery can take a while.",
					details: "restoring session mer-3",
				}}
			/>,
		);

		expect(screen.getByRole("status")).toHaveTextContent("Tallpine daemon is not ready yet");
		expect(screen.getByRole("status")).toHaveTextContent("Tallpine daemon is still starting");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Restart daemon" })).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Show details" }));
		expect(screen.getByText("restoring session mer-3")).toBeInTheDocument();
	});

	it("updates an already-mounted failure when the language changes", async () => {
		render(<DaemonFailureBanner status={{ state: "error", code: "not_ready" }} />);
		expect(screen.getByRole("button", { name: "Restart daemon" })).toBeInTheDocument();

		await act(() => appI18n.changeLanguage("zh-CN"));

		expect(screen.getByRole("button", { name: "重启守护进程" })).toBeInTheDocument();
		expect(screen.getByRole("alert")).toHaveTextContent("Tallpine 守护进程尚未就绪");
	});

	it("restarts a daemon that timed out during startup", async () => {
		const restart = vi.spyOn(tallpineBridge.daemon, "restart").mockResolvedValue({ state: "starting" });
		render(
			<DaemonFailureBanner
				status={{
					state: "error",
					code: "not_ready",
					message: "Tallpine daemon did not finish starting within 30 seconds.",
				}}
			/>,
		);

		fireEvent.click(screen.getByRole("button", { name: "Restart daemon" }));

		await waitFor(() => expect(restart).toHaveBeenCalledTimes(1));
	});

	it("shows a restart failure inline", async () => {
		vi.spyOn(tallpineBridge.daemon, "restart").mockRejectedValue(new Error("Daemon did not stop"));
		render(<DaemonFailureBanner status={{ state: "error", code: "not_ready" }} />);

		fireEvent.click(screen.getByRole("button", { name: "Restart daemon" }));

		expect(await screen.findByText("Daemon did not stop")).toBeInTheDocument();
	});

	it("explains a start that waits for the previous daemon, without a restart button", () => {
		render(
			<DaemonFailureBanner
				status={{
					state: "starting",
					phase: "datadir_lock_wait",
					lockHolderPid: 87127,
					message: "Waiting for the previous Tallpine daemon (pid 87127) to finish shutting down.",
				}}
			/>,
		);

		const status = screen.getByRole("status");
		expect(status).toHaveTextContent("Tallpine is waiting for the previous daemon");
		expect(status).toHaveTextContent("Waiting for the previous Tallpine daemon (pid 87127) to finish shutting down.");
		expect(status).toHaveTextContent("Tallpine starts as soon as the previous daemon exits.");
		expect(screen.queryByRole("button", { name: /Retry|Restart daemon/ })).toBeNull();
	});

	it("shows the lock refusal, the daemon's error and a Retry that restarts", async () => {
		const restart = vi.spyOn(tallpineBridge.daemon, "restart").mockResolvedValue({ state: "starting" });
		const daemonError = "data dir is in use by another Tallpine process: /d (pid 87127); waited 1m48s for it to exit, refusing to start";
		render(
			<DaemonFailureBanner
				placement="inline"
				status={{
					state: "stopped",
					code: "datadir_locked",
					lockHolderPid: 87127,
					message: "The previous Tallpine daemon (pid 87127) is still shutting down, so a new daemon could not start.",
					daemonError,
				}}
			/>,
		);

		const alert = screen.getByRole("alert");
		expect(alert).toHaveTextContent("Tallpine daemon could not start");
		expect(alert).toHaveTextContent("The previous Tallpine daemon (pid 87127) is still shutting down");
		expect(alert).toHaveTextContent(daemonError);
		expect(alert).toHaveTextContent("retries automatically when that process exits");
		expect(alert.className).not.toContain("fixed");
		fireEvent.click(screen.getByRole("button", { name: "Retry" }));
		await waitFor(() => expect(restart).toHaveBeenCalledTimes(1));
	});

	it("shows the daemon's own error for any other failed start", () => {
		render(
			<DaemonFailureBanner
				status={{ state: "stopped", code: "exited", message: "Daemon exited with code 1", daemonError: "open store: disk I/O error" }}
			/>,
		);

		expect(screen.getByRole("alert")).toHaveTextContent("open store: disk I/O error");
		expect(screen.getByRole("button", { name: "Restart daemon" })).toBeInTheDocument();
	});
});
