import { describe, expect, it, vi } from "vitest";
import { attachWebAuthnAccountChooser, createWebAuthnAccountChooser } from "./browser-webauthn-chooser";

const accounts = [
	{ credentialId: "cred-a", name: "alice@example.com", userHandle: "a" },
	{ credentialId: "cred-b", displayName: "Bob", userHandle: "b" },
	{ credentialId: "cred-c", userHandle: "c" },
] as never[];

function fakeSession() {
	const listeners: Array<(...args: unknown[]) => void> = [];
	return {
		listeners,
		on: vi.fn((event: string, listener: (...args: unknown[]) => void) => {
			if (event === "select-webauthn-account") listeners.push(listener);
		}),
	};
}

describe("createWebAuthnAccountChooser", () => {
	it("lists each passkey plus Cancel and returns the picked credential", async () => {
		const showMessageBox = vi.fn(async () => ({ response: 1 }));
		const choose = createWebAuthnAccountChooser(showMessageBox);

		await expect(choose("example.com", accounts)).resolves.toBe("cred-b");
		expect(showMessageBox).toHaveBeenCalledWith(
			expect.objectContaining({
				message: "Choose a passkey for example.com",
				buttons: ["alice@example.com", "Bob", "Passkey 3", "Cancel"],
				cancelId: 3,
			}),
		);
	});

	it("returns undefined when the person cancels", async () => {
		const choose = createWebAuthnAccountChooser(async () => ({ response: 3 }));
		await expect(choose("example.com", accounts)).resolves.toBeUndefined();
	});
});

describe("attachWebAuthnAccountChooser", () => {
	it("answers select-webauthn-account once with the chosen credential", async () => {
		const session = fakeSession();
		attachWebAuthnAccountChooser(session, async () => "cred-a");
		const callback = vi.fn();

		session.listeners[0]!({}, { relyingPartyId: "example.com", accounts }, callback);
		await vi.waitFor(() => expect(callback).toHaveBeenCalledTimes(1));
		expect(callback).toHaveBeenCalledWith("cred-a");
	});

	it("cancels the request when the chooser fails", async () => {
		const session = fakeSession();
		attachWebAuthnAccountChooser(session, async () => {
			throw new Error("dialog failed");
		});
		const callback = vi.fn();

		session.listeners[0]!({}, { relyingPartyId: "example.com", accounts }, callback);
		await vi.waitFor(() => expect(callback).toHaveBeenCalledTimes(1));
		expect(callback).toHaveBeenCalledWith();
	});

	it("attaches one listener per session however many tabs share it", () => {
		const session = fakeSession();
		attachWebAuthnAccountChooser(session, async () => undefined);
		attachWebAuthnAccountChooser(session, async () => undefined);

		expect(session.listeners).toHaveLength(1);
	});

	it("skips sessions without event support and hosts without a chooser", () => {
		expect(() => attachWebAuthnAccountChooser({}, async () => undefined)).not.toThrow();
		const session = fakeSession();
		attachWebAuthnAccountChooser(session, undefined);
		expect(session.on).not.toHaveBeenCalled();
	});
});
