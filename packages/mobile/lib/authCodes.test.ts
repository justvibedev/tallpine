import { describe, expect, it } from "vitest";
import { revokesPairing } from "./authCodes";
import { classifyConnectionFailure, shouldKeepPolling } from "./connectionError";

describe("classifying 401/403 by the envelope code", () => {
	it("keeps credential codes as auth", () => {
		for (const code of ["DEVICE_TOKEN_REVOKED", "BAD_PASSWORD", "LEGACY_PAIRING_EXPIRED", "BAD_PAIRING_CODE"]) {
			expect(classifyConnectionFailure(401, code)).toBe("auth");
		}
		expect(classifyConnectionFailure(403, "BAD_PASSWORD")).toBe("auth");
	});
	it("does not call a policy 403 a rotated password", () => {
		expect(classifyConnectionFailure(403, "ROUTE_FORBIDDEN")).toBe("forbidden");
		expect(classifyConnectionFailure(401, "GITHUB_AUTH_INVALID")).toBe("forbidden");
	});
	it("gives encrypted-transport and used-code their own failures", () => {
		expect(classifyConnectionFailure(403, "ENCRYPTED_TRANSPORT_REQUIRED")).toBe("encrypted-required");
		expect(classifyConnectionFailure(401, "PAIRING_CODE_USED")).toBe("code-used");
	});
	it("falls back to the status when no code was sent", () => {
		expect(classifyConnectionFailure(401)).toBe("auth");
		expect(classifyConnectionFailure(403)).toBe("auth");
	});
	it("keeps polling through a plain forbidden route, but not through auth", () => {
		expect(shouldKeepPolling(403, "ROUTE_FORBIDDEN")).toBe(true);
		expect(shouldKeepPolling(403, "ENCRYPTED_TRANSPORT_REQUIRED")).toBe(false);
		expect(shouldKeepPolling(401, "BAD_PASSWORD")).toBe(false);
	});
});

describe("which 401s revoke the pairing", () => {
	it("only the credential codes do", () => {
		expect(revokesPairing("DEVICE_TOKEN_REVOKED")).toBe(true);
		expect(revokesPairing("BAD_PASSWORD")).toBe(true);
		expect(revokesPairing("LEGACY_PAIRING_EXPIRED")).toBe(true);
		expect(revokesPairing("GITHUB_AUTH_INVALID")).toBe(false);
		expect(revokesPairing("SESSION_TOKEN_INVALID")).toBe(false);
		expect(revokesPairing(undefined)).toBe(false);
	});
});
