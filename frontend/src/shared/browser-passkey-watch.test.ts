import { afterEach, describe, expect, it, vi } from "vitest";
import { installPasskeyRequestWatch } from "./browser-passkey-watch";

type Deferred = { resolve: (value: unknown) => void; reject: (error: unknown) => void };

// A stand-in for CredentialsContainer: methods live on the prototype, as in
// Chromium, and each call returns a promise the test settles.
function fakeCredentials() {
	const calls: Deferred[] = [];
	class FakeCredentialsContainer {}
	for (const name of ["get", "create"]) {
		Object.defineProperty(FakeCredentialsContainer.prototype, name, {
			configurable: true,
			enumerable: true,
			writable: true,
			value: function () {
				return new Promise((resolve, reject) => calls.push({ resolve, reject }));
			},
		});
	}
	const container = new FakeCredentialsContainer() as { get: (o?: unknown) => Promise<unknown>; create: (o?: unknown) => Promise<unknown> };
	vi.stubGlobal("navigator", { credentials: container });
	return { calls, container, proto: FakeCredentialsContainer.prototype };
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
const publicKey = { challenge: new Uint8Array(32) };

afterEach(() => vi.unstubAllGlobals());

describe("installPasskeyRequestWatch", () => {
	it("reports a passkey get as pending until it settles", async () => {
		const { calls, container } = fakeCredentials();
		const report = vi.fn();
		installPasskeyRequestWatch(report);

		const result = container.get({ publicKey });
		expect(report.mock.calls).toEqual([[true]]);

		calls[0]!.resolve("credential");
		await expect(result).resolves.toBe("credential");
		await flush();
		expect(report.mock.calls).toEqual([[true], [false]]);
	});

	it("settles on rejection and stays pending while overlapping requests remain", async () => {
		const { calls, container } = fakeCredentials();
		const report = vi.fn();
		installPasskeyRequestWatch(report);

		const first = container.create({ publicKey });
		const second = container.get({ publicKey });
		calls[0]!.reject(new Error("NotAllowedError"));
		await expect(first).rejects.toThrow("NotAllowedError");
		await flush();
		expect(report.mock.calls).toEqual([[true]]);

		calls[1]!.resolve(null);
		await second;
		await flush();
		expect(report.mock.calls).toEqual([[true], [false]]);
	});

	it("ignores conditional (autofill) mediation and non-passkey credential requests", async () => {
		const { container } = fakeCredentials();
		const report = vi.fn();
		installPasskeyRequestWatch(report);

		void container.get({ publicKey, mediation: "conditional" });
		void container.get({ password: true });
		void container.get();

		expect(report).not.toHaveBeenCalled();
	});

	it("keeps the prototype property attributes when wrapping", () => {
		const { proto } = fakeCredentials();
		const before = Object.getOwnPropertyDescriptor(proto, "get");
		installPasskeyRequestWatch(vi.fn());
		const after = Object.getOwnPropertyDescriptor(proto, "get");

		expect(after?.value).not.toBe(before?.value);
		expect({ ...after, value: undefined }).toEqual({ ...before, value: undefined });
	});

	it("does nothing without navigator.credentials", () => {
		vi.stubGlobal("navigator", {});
		expect(() => installPasskeyRequestWatch(vi.fn())).not.toThrow();
	});
});
