import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { LspBannerCard } from "./LspBannerCard";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock }, apiErrorMessage: () => "request failed" }));

const rec = (over: Record<string, unknown> = {}) => ({ language: "go", files: 9, pluginId: "gopls-lsp@x", pluginInstalled: true, pluginInstall: "i", binary: "gopls", binaryInstalled: true, binaryProblem: "", binaryInstall: "b", roles: [{ role: "worker", on: true }], ...over });
let lsp: Record<string, unknown>;
beforeEach(() => {
	window.localStorage.clear();
	useUiStore.setState({ settingsModal: null });
	lsp = { recommendations: [rec({ pluginInstalled: false, roles: [{ role: "worker", on: false }] })], mismatches: [] };
	getMock.mockReset().mockImplementation(async () => ({ data: { offers: [], lsp } }));
});
const ui = (id = "p") => <QueryClientProvider client={new QueryClient()}><LspBannerCard projectId={id} /></QueryClientProvider>;
const settle = async () => { await act(async () => { await new Promise((r) => setTimeout(r, 20)); }); };

it("shows once for a recommendation, and Not now keeps it gone", async () => {
	const { unmount } = render(ui());
	await screen.findByTestId("lsp-banner");
	await userEvent.click(screen.getByRole("button", { name: "Not now" }));
	expect(screen.queryByTestId("lsp-banner")).not.toBeInTheDocument();
	unmount();
	render(ui());
	await settle();
	expect(screen.queryByTestId("lsp-banner")).not.toBeInTheDocument();
});

it("Review opens Code intelligence settings and persists", async () => {
	const { unmount } = render(ui());
	await userEvent.click(await screen.findByRole("button", { name: "Review" }));
	expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "project", projectId: "p", section: "codeIntelligence" });
	unmount();
	render(ui());
	await settle();
	expect(screen.queryByTestId("lsp-banner")).not.toBeInTheDocument();
});

it("is per project", async () => {
	window.localStorage.setItem("tallpine.lspBanner.other", "1");
	render(ui());
	expect(await screen.findByTestId("lsp-banner")).toBeInTheDocument();
});

it("shows for a worker-disabled plugin and a mismatch", async () => {
	lsp = { recommendations: [rec({ roles: [{ role: "worker", on: false }] })], mismatches: [] };
	const a = render(ui());
	expect(await screen.findByTestId("lsp-banner")).toHaveTextContent("not enabled for workers");
	a.unmount();
	lsp = { recommendations: [], mismatches: [{ pluginId: "x", language: "rust", roles: ["worker"] }] };
	render(ui("q"));
	expect(await screen.findByTestId("lsp-banner")).toHaveTextContent("does not use");
});

it("stays away when nothing is recommended", async () => {
	lsp = { recommendations: [rec()], mismatches: [] };
	render(ui());
	await settle();
	expect(screen.queryByTestId("lsp-banner")).not.toBeInTheDocument();
});

it("re-reads the resolved state when the project changes", async () => {
	window.localStorage.setItem("tallpine.lspBanner.p", "1");
	const wrap = (id: string) => <QueryClientProvider client={client}><LspBannerCard projectId={id} /></QueryClientProvider>;
	const client = new QueryClient();
	const { rerender } = render(wrap("p"));
	await settle();
	expect(screen.queryByTestId("lsp-banner")).not.toBeInTheDocument();
	rerender(wrap("q"));
	expect(await screen.findByTestId("lsp-banner")).toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Not now" }));
	rerender(wrap("r"));
	expect(await screen.findByTestId("lsp-banner")).toBeInTheDocument();
});

it("reminds about a plugin that is on but not installed even after an earlier dismissal, until it is installed", async () => {
	window.localStorage.setItem("tallpine.lspBanner.p", "1");
	lsp = { recommendations: [rec({ binaryInstalled: false })], mismatches: [] };
	const { unmount } = render(ui());
	expect(await screen.findByTestId("lsp-banner")).toHaveTextContent("gopls-lsp@x or its language server is not installed");
	expect(screen.queryByRole("button", { name: "More options" })).not.toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Not now" }));
	expect(screen.queryByTestId("lsp-banner")).not.toBeInTheDocument();
	unmount();
	const again = render(ui());
	await screen.findByTestId("lsp-banner");
	again.unmount();
	lsp = { recommendations: [rec()], mismatches: [] };
	render(ui());
	await settle();
	expect(screen.queryByTestId("lsp-banner")).not.toBeInTheDocument();
});
