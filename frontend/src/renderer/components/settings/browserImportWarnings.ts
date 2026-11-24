import type { BrowserImportWarning } from "../../../shared/browser-profile-import";
import { appI18n, type MessageKey } from "../../i18n";

export function browserImportWarningText(warning: BrowserImportWarning): string {
	return appI18n.t(warningKey(warning.code), {
		count: warning.count ?? 0,
		sites: (warning.sites ?? []).join(", "),
	});
}

function warningKey(code: BrowserImportWarning["code"]): MessageKey {
	switch (code) {
		case "cookie-database-missing": return "settings.browserImport.warning.cookie-database-missing";
		case "history-database-missing": return "settings.browserImport.warning.history-database-missing";
		case "isolated-cookies-skipped": return "settings.browserImport.warning.isolated-cookies-skipped";
		case "cookie-limit-truncated": return "settings.browserImport.warning.cookie-limit-truncated";
		case "history-limit-truncated": return "settings.browserImport.warning.history-limit-truncated";
		case "encrypted-cookies-skipped": return "settings.browserImport.warning.encrypted-cookies-skipped";
		case "expired-cookies-skipped": return "settings.browserImport.warning.expired-cookies-skipped";
		case "invalid-cookies-skipped": return "settings.browserImport.warning.invalid-cookies-skipped";
		case "cookie-attributes-defaulted": return "settings.browserImport.warning.cookie-attributes-defaulted";
		case "cookie-write-failed": return "settings.browserImport.warning.cookie-write-failed";
		case "device-bound-sessions": return "settings.browserImport.warning.device-bound-sessions";
	}
}
