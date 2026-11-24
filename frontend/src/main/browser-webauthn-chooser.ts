import type { WebAuthnAccount } from "electron";

type MessageBox = (options: {
	type: "question";
	title: string;
	message: string;
	detail: string;
	buttons: string[];
	defaultId: number;
	cancelId: number;
}) => Promise<{ response: number }>;

/** Picks one of several passkeys a page's request matched; undefined cancels. */
export type WebAuthnAccountChooser = (relyingPartyId: string, accounts: WebAuthnAccount[]) => Promise<string | undefined>;

type SessionWithWebAuthnEvents = {
	on(
		event: "select-webauthn-account",
		listener: (
			event: unknown,
			details: { relyingPartyId: string; accounts: WebAuthnAccount[] },
			callback: (credentialId?: string | null) => void,
		) => void,
	): unknown;
};

// Native picker for Electron's select-webauthn-account event. Without a
// listener Electron cancels any request that matches more than one passkey,
// for example a security key holding several accounts for the same site.
export function createWebAuthnAccountChooser(showMessageBox: MessageBox): WebAuthnAccountChooser {
	return async (relyingPartyId, accounts) => {
		const labels = accounts.map((account, index) => account.name || account.displayName || `Passkey ${index + 1}`);
		const result = await showMessageBox({
			type: "question",
			title: "Choose a passkey",
			message: `Choose a passkey for ${relyingPartyId}`,
			detail: "This site found more than one passkey on your security key or device.",
			buttons: [...labels, "Cancel"],
			defaultId: 0,
			cancelId: labels.length,
		});
		return accounts[result.response]?.credentialId;
	};
}

// Electron reuses one Session per partition across windows and tabs, so the
// listener is attached once per Session for the life of the process.
const attachedSessions = new WeakSet<object>();

export function attachWebAuthnAccountChooser(session: unknown, choose: WebAuthnAccountChooser | undefined): void {
	if (!choose || !session || typeof (session as Partial<SessionWithWebAuthnEvents>).on !== "function") return;
	if (attachedSessions.has(session)) return;
	attachedSessions.add(session);
	(session as SessionWithWebAuthnEvents).on("select-webauthn-account", (_event, details, callback) => {
		// The request stays pending until the callback runs, so it runs exactly once.
		choose(details.relyingPartyId, details.accounts).then(
			(credentialId) => callback(credentialId),
			() => callback(),
		);
	});
}
