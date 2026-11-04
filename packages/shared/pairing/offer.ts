import { toBase64Url } from "./base64url";

/** The wire contract of the pairing code: the desktop builds it, the phone parses it. */
export type PairingEndpoint = {
	/** A plain string so a newer daemon's kind passes through; the phone validates it. */
	kind: string;
	host: string;
	port: number;
	secure: boolean;
	/** SHA-256 of the gateway certificate's public key; the phone pins it. */
	pin?: string;
};

export type PairingOffer<E extends PairingEndpoint = PairingEndpoint> = {
	v: 2;
	/** Empty for a legacy code: the machine adopts an id on its first connect. */
	hostId: string;
	name: string;
	platform: string;
	endpoints: E[];
	token: string;
};

export type PairingOfferInput = {
	endpoints: readonly PairingEndpoint[];
	password: string;
	hostId: string;
	name: string;
	platform: string;
};

/**
 * Assembles the offer for a QR or a copyable code. Carries every advertised
 * endpoint because the phone races them. Throws on an empty list: a code with
 * nothing to connect to looks scannable and cannot work.
 */
export function buildPairingOffer(input: PairingOfferInput): PairingOffer {
	if (input.endpoints.length === 0) {
		throw new Error("cannot build a pairing offer with no endpoints");
	}
	return {
		v: 2,
		hostId: input.hostId,
		name: input.name,
		platform: input.platform,
		endpoints: [...input.endpoints],
		token: input.password,
	};
}

/** Encodes an offer for a QR or a copyable code. */
export function encodePairingCode(offer: PairingOffer): string {
	return toBase64Url(JSON.stringify(offer));
}

/** Wraps an offer in a link. The payload goes in the fragment, never the query, so the token stays out of web logs. */
export function pairingCodeUrl(offer: PairingOffer, base: string): string {
	return `${base}#${encodePairingCode(offer)}`;
}
