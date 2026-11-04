import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render as renderView, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConnectMobileContent } from "./ConnectMobileContent";
import { TooltipProvider } from "../ui/tooltip";

const test = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: test.get, POST: test.post },
	apiErrorMessage: (e: unknown) => String(e),
}));
vi.mock("../../hooks/useStayAwake", () => ({
	useStayAwake: () => ({ on: false, lidOn: false, supported: false, error: null, set: vi.fn(), setLid: vi.fn() }),
}));

function status(overrides: Record<string, unknown> = {}) {
	return {
		featureEnabled: false,
		publicBaseUrl: "",
		quickTunnel: false,
		enabled: false,
		noPublicTunnel: true,
		host: "",
		tailscaleHost: "",
		port: 0,
		password: "",
		warning: "",
		endpoints: [],
		tunnel: { supported: false, running: false, ready: false, hostname: "", location: "", lastError: "" },
		securePairing: { enabled: false, available: false, active: false, host: "", port: 0, reason: "" },
		...overrides,
	};
}

function view() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return renderView(
		<QueryClientProvider client={client}>
			<TooltipProvider delayDuration={0}>
				<ConnectMobileContent active />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("ConnectMobileContent feature flag", () => {
	beforeEach(() => {
		test.get.mockReset();
		test.post.mockReset();
	});

	it("shows only the master toggle while mobile is off", async () => {
		test.get.mockResolvedValue({ data: status() });
		view();
		const toggle = await screen.findByRole("switch", { name: "Enable mobile access" });
		expect(toggle).not.toBeChecked();
		expect(screen.queryByTestId("mobile-pairing")).toBeNull();
		expect(screen.queryByTestId("mobile-public-url")).toBeNull();
		expect(screen.queryByTestId("mobile-quick-tunnel")).toBeNull();
	});

	it("turning it on posts the flag", async () => {
		test.get.mockResolvedValue({ data: status() });
		test.post.mockResolvedValue({ data: status({ featureEnabled: true }) });
		view();
		await userEvent.click(await screen.findByRole("switch", { name: "Enable mobile access" }));
		await waitFor(() => expect(test.post).toHaveBeenCalledWith("/api/v1/mobile/feature", { body: { enabled: true } }));
	});

	const openAdvanced = async () => userEvent.click(await screen.findByTestId("mobile-advanced-toggle"));

	it("keeps the base URL and the warned, off-by-default quick tunnel under Advanced", async () => {
		test.get.mockResolvedValue({ data: status({ featureEnabled: true }) });
		view();
		await screen.findByTestId("mobile-advanced-toggle");
		expect(screen.queryByTestId("mobile-public-url")).toBeNull();
		expect(screen.queryByTestId("mobile-quick-tunnel")).toBeNull();
		await openAdvanced();
		expect(await screen.findByTestId("mobile-public-url")).toBeInTheDocument();
		const tunnel = await screen.findByTestId("mobile-quick-tunnel");
		expect(tunnel).toHaveTextContent("Cloudflare terminates TLS");
		expect(screen.getByRole("switch", { name: "Cloudflare quick tunnel" })).not.toBeChecked();
		// No install prompt for a tunnel the user never asked for.
		expect(screen.queryByTestId("mobile-remote-unavailable")).toBeNull();
	});

	it("has no secure-pairing switch or copy: HTTPS is always on", async () => {
		const on = status({ featureEnabled: true, enabled: true, securePairing: { enabled: true, available: true, active: true, host: "", port: 443, reason: "" } });
		test.get.mockResolvedValue({ data: on });
		const { container } = view();
		await openAdvanced();
		await screen.findByTestId("mobile-public-url");
		expect(screen.queryByRole("switch", { name: /secure pairing/i })).toBeNull();
		expect(container.textContent).not.toMatch(/secure pairing|TLS, pinned|LAN/i);
		// The method tabs are gone too.
		expect(screen.queryByRole("radio", { name: "LAN" })).toBeNull();
		expect(screen.queryByRole("radiogroup")).toBeNull();
	});

	it("turns HTTPS on by itself on a tailnet, and never offers to turn it off", async () => {
		const off = status({ featureEnabled: true, tailscaleHost: "100.1.1.1", securePairing: { enabled: false, available: true, active: false, host: "", port: 0, reason: "" } });
		test.get.mockResolvedValue({ data: off });
		test.post.mockResolvedValue({ data: off });
		window.localStorage.setItem("tallpine.mobile.securePairingOptOut", "1");
		view();
		// A stale opt-out from the removed switch must not keep HTTPS off.
		await waitFor(() => expect(test.post).toHaveBeenCalledWith("/api/v1/mobile/secure-pairing", { body: { enabled: true } }));
		window.localStorage.clear();
	});

	it("shows why HTTPS on the tailnet did not start", async () => {
		test.get.mockResolvedValue({
			data: status({ featureEnabled: true, enabled: true, tailscaleHost: "100.1.1.1", securePairing: { enabled: true, available: true, active: false, host: "", port: 0, reason: "serve_failed" } }),
		});
		view();
		expect(await screen.findByTestId("secure-pairing-reason")).toBeInTheDocument();
	});

	it("shows the whole security code, upper-case in groups of four, under Advanced", async () => {
		const pin = "ab12cd34ef567890ab12cd34ef567890ab12cd34ef567890ab12cd34ef567890";
		test.get.mockResolvedValue({
			data: status({ featureEnabled: true, enabled: true, tlsPin: pin, endpoints: [{ kind: "lan", host: "192.168.1.5", port: 4000, secure: true }] }),
		});
		view();
		await screen.findByTestId("mobile-advanced-toggle");
		expect(screen.queryByTestId("mobile-security-code")).toBeNull();
		await openAdvanced();
		const expected = pin.toUpperCase().replace(/(.{4})(?=.)/g, "$1 ");
		expect(await screen.findByTestId("mobile-security-code")).toHaveTextContent(expected);
		expect(expected.replace(/ /g, "")).toHaveLength(64);
	});

	it("saves the typed base URL", async () => {
		test.get.mockResolvedValue({ data: status({ featureEnabled: true }) });
		test.post.mockResolvedValue({ data: status({ featureEnabled: true, publicBaseUrl: "https://ao.example.com:443" }) });
		view();
		await openAdvanced();
		const input = await screen.findByRole("textbox", { name: "Reachable address" });
		await userEvent.type(input, "https://ao.example.com");
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() =>
			expect(test.post).toHaveBeenCalledWith("/api/v1/mobile/public-url", { body: { url: "https://ao.example.com" } }),
		);
	});

	it("shows per-device pairing on by default and lets the user turn it off", async () => {
		test.get.mockResolvedValue({ data: status({ featureEnabled: true, enabled: true, requireDevicePairing: true }) });
		test.post.mockResolvedValue({ data: status({ featureEnabled: true, enabled: true, requireDevicePairing: false }) });
		view();
		await openAdvanced();
		const row = await screen.findByTestId("mobile-require-device-pairing");
		expect(row).toHaveTextContent("older fixed key stop working");
		const toggle = screen.getByRole("switch", { name: "Require per-device pairing (blocks older mobile apps)" });
		expect(toggle).toBeChecked();
		await userEvent.click(toggle);
		await waitFor(() => expect(test.post).toHaveBeenCalledWith("/api/v1/mobile/device-pairing", { body: { enabled: false } }));
	});

	it("warns while an old fixed password still authenticates a phone", async () => {
		test.get.mockResolvedValue({ data: status({ featureEnabled: true, enabled: true, legacyPasswordActive: true }) });
		view();
		await openAdvanced();
		expect(await screen.findByTestId("mobile-legacy-password-warning")).toHaveTextContent("not the pairing code above");
	});

	it("shows no legacy-password warning once none is active", async () => {
		test.get.mockResolvedValue({ data: status({ featureEnabled: true, enabled: true, legacyPasswordActive: false }) });
		view();
		await openAdvanced();
		await screen.findByTestId("mobile-require-device-pairing");
		expect(screen.queryByTestId("mobile-legacy-password-warning")).toBeNull();
	});

	// The displayed value is a single-use code for the pair exchange, not a
	// bearer password; calling it "Password" sent people to type it into manual
	// connect, where it is refused. The store links pointed at the official AO
	// apps, which cannot pair with Tallpine.
	it("labels the single-use pairing code, links no store app and says nothing of AO", async () => {
		test.get.mockResolvedValue({
			data: status({
				featureEnabled: true,
				enabled: true,
				host: "192.168.1.196",
				port: 65221,
				password: "PEpQDzOF",
				hostId: "h_1",
				endpoints: [{ kind: "lan", host: "192.168.1.196", port: 65221, secure: false }],
			}),
		});
		const { container } = view();
		expect(await screen.findByTestId("mobile-pairing-code-hint")).toHaveTextContent("Single use");
		expect(screen.getByText("Pairing code")).toBeInTheDocument();
		expect(screen.queryByText("Password")).toBeNull();
		expect(screen.queryByText("Get the app")).toBeNull();
		expect(screen.queryByTestId("ios-store-qr")).toBeNull();
		expect(container.textContent).not.toMatch(/\bAO\b/);
	});
});
