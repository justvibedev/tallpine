import { afterEach, describe, expect, it, vi } from "vitest";
import { RoleSettingsUnsupportedError, setProjectRoleSettings } from "../api";
import type { ServerConfig } from "../config";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));

const config: ServerConfig = { host: "192.0.2.1", secure: true, httpPort: "3011", muxPort: "14801", password: "secret" };

const answer = (status: number, body: unknown) =>
	vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(body), { status, statusText: "x" })));

describe("setProjectRoleSettings", () => {
	afterEach(() => vi.unstubAllGlobals());

	it("PATCHes the narrow route with only the patch and returns the project", async () => {
		answer(200, { project: { id: "p", name: "P" } });
		const out = await setProjectRoleSettings(config, "p/1", { worker: { effort: "low" } });
		expect(out.id).toBe("p");
		const [url, init] = (fetch as unknown as ReturnType<typeof vi.fn>).mock.calls[0];
		expect(String(url)).toContain("/api/v1/projects/p%2F1/role-settings");
		expect(init.method).toBe("PATCH");
		expect(JSON.parse(init.body)).toEqual({ worker: { effort: "low" } });
	});

	it("explains an older daemon that lacks the route instead of crashing", async () => {
		answer(404, { code: "NOT_FOUND", message: "not found" });
		await expect(setProjectRoleSettings(config, "p", { worker: { effort: "low" } })).rejects.toBeInstanceOf(RoleSettingsUnsupportedError);
	});

	it("keeps a missing project distinct from a missing route", async () => {
		answer(404, { code: "PROJECT_NOT_FOUND", message: "Unknown project" });
		await expect(setProjectRoleSettings(config, "p", { worker: { effort: "low" } })).rejects.not.toBeInstanceOf(RoleSettingsUnsupportedError);
	});
});
