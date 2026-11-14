import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { DaemonPeerCheckSection } from "./DaemonPeerCheckSection";

const { patchMock, state } = vi.hoisted(() => ({
	patchMock: vi.fn(),
	state: { peerCheck: undefined as undefined | { active: boolean; mode: string; modeReason: string } },
}));
vi.mock("../../lib/api-client", () => ({
	apiClient: { PATCH: patchMock },
	apiErrorMessage: (error: { message?: string }) => error?.message ?? "error",
}));
vi.mock("../../hooks/useSilentFailures", () => ({
	silentFailuresQueryKey: ["health", "silent-failures"],
	useSilentFailures: () => ({ data: state.peerCheck ? { peerCheck: { admitted: {}, wouldRefuse: [], overflow: 0, dropped: 0, ...state.peerCheck } } : {} }),
}));

function renderSection() {
	return render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<DaemonPeerCheckSection />
		</QueryClientProvider>,
	);
}

const label = "Refuse other apps on this Mac";

const macLabel = label;
const computerLabel = "Refuse other apps on this computer";

// "this Mac" vs "this computer" follows the user agent (lib/this-computer.ts).
function setPlatform(platform: string) {
	vi.spyOn(window.navigator, "platform", "get").mockReturnValue(platform);
}

beforeEach(() => {
	vi.restoreAllMocks();
	setPlatform("MacIntel");
	patchMock.mockReset();
	patchMock.mockResolvedValue({ data: {}, error: undefined });
	state.peerCheck = { active: true, mode: "enforce", modeReason: "signed build" };
});

describe("DaemonPeerCheckSection", () => {
	it("says this computer, not this Mac, on Windows", () => {
		setPlatform("Win32");
		renderSection();
		expect(screen.getByRole("switch", { name: computerLabel })).toBeChecked();
		expect(screen.getByTestId("peer-check-status")).toHaveTextContent("On: other apps on this computer are refused.");
		expect(screen.queryByText(/this Mac/)).toBeNull();
	});

	it("keeps this Mac on macOS", () => {
		renderSection();
		expect(screen.getByRole("switch", { name: macLabel })).toBeChecked();
	});

	it("shows the check on and turns it off through the settings registry", async () => {
		renderSection();
		expect(screen.getByRole("switch", { name: label })).toBeChecked();
		expect(screen.getByTestId("peer-check-status")).toHaveTextContent("On: other apps on this Mac are refused.");
		await userEvent.setup().click(screen.getByRole("switch", { name: label }));
		await waitFor(() =>
			expect(patchMock).toHaveBeenCalledWith("/api/v1/settings/values", { body: { set: { "app.daemon.peerCheck": "off" } } }),
		);
	});

	it("shows the check off and turns it back to the default", async () => {
		state.peerCheck = { active: true, mode: "off", modeReason: "turned off in the desktop app" };
		renderSection();
		expect(screen.getByRole("switch", { name: label })).not.toBeChecked();
		expect(screen.getByTestId("peer-check-status")).toHaveTextContent("Off: any process holding a daemon token is let in.");
		await userEvent.setup().click(screen.getByRole("switch", { name: label }));
		await waitFor(() =>
			expect(patchMock).toHaveBeenCalledWith("/api/v1/settings/values", { body: { set: { "app.daemon.peerCheck": "default" } } }),
		);
	});

	it("offers no switch on a build that cannot check callers, and says why", () => {
		state.peerCheck = { active: false, mode: "off", modeReason: "unsigned or development build" };
		renderSection();
		expect(screen.queryByRole("switch")).toBeNull();
		expect(screen.getByTestId("peer-check-status")).toHaveTextContent("Not available in this build: unsigned or development build.");
	});

	it("shows the daemon's refusal", async () => {
		patchMock.mockResolvedValue({ data: undefined, error: { message: "only the desktop app may change app.daemon.peerCheck" } });
		renderSection();
		await userEvent.setup().click(screen.getByRole("switch", { name: label }));
		expect(await screen.findByRole("alert")).toHaveTextContent("only the desktop app");
	});

	it("renders nothing for a daemon without the check", () => {
		state.peerCheck = undefined;
		const { container } = renderSection();
		expect(container).toBeEmptyDOMElement();
	});
});
