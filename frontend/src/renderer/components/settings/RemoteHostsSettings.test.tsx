import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";

const saved = vi.hoisted(() => ({ entries: [] as Array<{ hostId: string; label: string; url: string }> }));
const account = vi.hoisted(() => ({ status: "authenticated" as "authenticated" | "unauthenticated" }));
const remotes = vi.hoisted(() => ({
	list: vi.fn(async () => saved.entries),
	add: vi.fn(async ({ label, url }: { label: string; url: string }) => {
		saved.entries = [{ hostId: "box-a", label, url }];
		return "online" as "online" | "incompatible";
	}),
	connect: vi.fn(async (url: string) => ({ hostId: "box-a", label: "Box A", url, base: "http://127.0.0.1:4000" })),
	disconnect: vi.fn(async () => undefined),
	remove: vi.fn(async () => undefined),
	update: vi.fn(async (_oldUrl: string, changes: { label?: string; url?: string }) => {
		saved.entries = saved.entries.map((host) => ({ ...host, label: changes.label ?? host.label, url: changes.url ?? host.url }));
		return "online" as const;
	}),
}));
vi.mock("../../lib/bridge", () => ({ tallpineBridge: { remotes } }));
vi.mock("../../hooks/useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: "" } }) }));

import { RemoteHostsSettings } from "./RemoteHostsSettings";

const renderSettings = () => render(<QueryClientProvider client={new QueryClient()}><RemoteHostsSettings /></QueryClientProvider>);

afterEach(() => {
	account.status = "authenticated";
	saved.entries = [];
	useUiStore.setState({ remoteHosts: false });
	remotes.connect.mockClear();
	remotes.list.mockClear();
	remotes.add.mockClear();
	remotes.update.mockClear();
	remotes.remove.mockClear();
});


it("does not connect a newly paired host after other computers is turned off", async () => {
	useUiStore.setState({ remoteHosts: true });
	let finishAdd: ((health: "online") => void) | undefined;
	remotes.add.mockImplementationOnce(() => new Promise((resolve) => { finishAdd = resolve; }));
	renderSettings();
	fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Box A" } });
	fireEvent.change(screen.getByRole("textbox", { name: "Address" }), { target: { value: "http://box-a:3101" } });
	fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "secret123" } });
	const submit = screen.getByRole("button", { name: "Add computer" });
	fireEvent.click(submit);
	expect(submit).toBeDisabled();
	act(() => useUiStore.setState({ remoteHosts: false }));
	await act(async () => finishAdd?.("online"));
	await waitFor(() => expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue(""));
	expect(remotes.connect).not.toHaveBeenCalled();
});

it("pairs a host and shows it in the saved-host list", async () => {
	renderSettings();
	expect(screen.getByRole("switch", { name: "Connect to other computers" })).toHaveAttribute("aria-checked", "false");
	fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Box A" } });
	fireEvent.change(screen.getByRole("textbox", { name: "Address" }), { target: { value: "http://box-a:3101" } });
	fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "secret123" } });
	fireEvent.click(screen.getByRole("button", { name: "Add computer" }));
	await waitFor(() => expect(screen.getByText("Box A")).toBeVisible());
	expect(screen.getByRole("switch", { name: "Connect to other computers" })).toHaveAttribute("aria-checked", "true");
	expect(remotes.add).toHaveBeenCalledWith({ label: "Box A", url: "http://box-a:3101", password: "secret123" });
	expect(screen.getByLabelText("Access key")).toHaveAttribute("type", "password");
});

it("explains an incompatible host without adding it", async () => {
	remotes.add.mockResolvedValueOnce("incompatible");
	renderSettings();
	fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Box A" } });
	fireEvent.change(screen.getByRole("textbox", { name: "Address" }), { target: { value: "http://box-a:3101" } });
	fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "secret123" } });
	fireEvent.click(screen.getByRole("button", { name: "Add computer" }));
	await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Update Tallpine"));
	expect(saved.entries).toEqual([]);
});

it("explains how to re-pair a saved host without an identity", async () => {
	saved.entries = [{ hostId: "", label: "Old Box", url: "http://old-box:3101" }];
	renderSettings();
	await screen.findByText(/Old Box/);
	expect(screen.getByText(/edit this computer and enter its access key/i)).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Edit Old Box" }));
	expect(screen.getByLabelText("Access key")).toBeRequired();
	fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "new-secret" } });
	fireEvent.click(screen.getByRole("button", { name: "Save computer" }));
	await waitFor(() => expect(remotes.add).toHaveBeenCalledWith({
		label: "Old Box",
		url: "http://old-box:3101",
		password: "new-secret",
	}));
	expect(remotes.update).not.toHaveBeenCalled();
});

it("edits a saved host without reading or replacing its password", async () => {
	saved.entries = [{ hostId: "box-a", label: "Box A", url: "http://box-a:3101" }];
	renderSettings();
	await screen.findByRole("button", { name: "Edit Box A" });
	fireEvent.click(screen.getByRole("button", { name: "Edit Box A" }));
	expect(screen.getByLabelText("Access key")).toHaveValue("");
	expect(screen.getByLabelText("Access key")).not.toBeRequired();
	fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Office" } });
	fireEvent.change(screen.getByRole("textbox", { name: "Address" }), { target: { value: "http://office:3011" } });
	fireEvent.click(screen.getByRole("button", { name: "Save computer" }));
	await waitFor(() => expect(remotes.update).toHaveBeenCalledWith("http://box-a:3101", {
		label: "Office",
		url: "http://office:3011",
	}));
	expect(await screen.findByText("Office")).toBeVisible();
});

it("requires confirmation before forgetting a host", async () => {
	saved.entries = [{ hostId: "box-a", label: "Box A", url: "http://box-a:3101" }];
	remotes.remove.mockImplementationOnce(async () => { saved.entries = []; });
	renderSettings();
	fireEvent.click(await screen.findByRole("button", { name: "Remove Box A" }));
	expect(remotes.remove).not.toHaveBeenCalled();
	expect(screen.getByText(/Sessions keep running on the other computer/i)).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Remove" }));
	await waitFor(() => expect(remotes.remove).toHaveBeenCalledWith("http://box-a:3101"));
	expect(await screen.findByText("No other computers yet. Add one below.")).toBeVisible();
});
