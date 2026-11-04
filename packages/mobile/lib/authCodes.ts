/**
 * Envelope codes the daemon uses for credential problems. Classification and
 * revocation key on these rather than on the bare status: a 401 or 403 can also
 * mean an unrelated route or policy refusal, and treating every one as "the
 * password was rotated" sent people to re-scan a healthy pairing.
 */

/** The pairing this phone holds is no longer accepted by the machine. */
export const REVOKING_CODES: readonly string[] = ["DEVICE_TOKEN_REVOKED", "BAD_PASSWORD", "LEGACY_PAIRING_EXPIRED"];

/** Codes that make a 401/403 an authentication failure for UI purposes. */
export const AUTH_CODES: readonly string[] = [...REVOKING_CODES, "BAD_PAIRING_CODE"];

export const PAIRING_CODE_USED = "PAIRING_CODE_USED";
export const ENCRYPTED_TRANSPORT_REQUIRED = "ENCRYPTED_TRANSPORT_REQUIRED";

/** Whether a 401 with this envelope code means the pairing was dropped. */
export function revokesPairing(code: string | undefined): boolean {
	return code !== undefined && REVOKING_CODES.includes(code);
}

/** The gateway refuses plain http now that phones pin its certificate (HTTP 426). */
export const HTTPS_REQUIRED = "HTTPS_REQUIRED";
