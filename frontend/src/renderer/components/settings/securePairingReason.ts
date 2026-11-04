import type { TFunction } from "i18next";

// Maps a `securePairing.reason` from the daemon to the copy explaining it. An
// unknown reason (e.g. a newer daemon build) returns undefined and renders
// nothing rather than a raw translation key.
export function reasonMessage(reason: string, t: TFunction): string | undefined {
	switch (reason) {
		case "no_cli":
		case "no_magicdns":
			return t("mobile.securePairing.noCli");
		case "no_certs":
			return t("mobile.securePairing.noCerts");
		case "serve_failed":
			return t("mobile.securePairing.serveFailed");
		case "port_mismatch":
			return t("mobile.securePairing.portMismatch");
		default:
			return undefined;
	}
}
