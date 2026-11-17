import { PRODUCT_NAME } from "../shared/brand";
import type { AppLocale } from "../shared/ui-locale";
// A tray-only catalog: importing the renderer's catalogs put every locale file
// (1.3 MB) into main.js. Regenerate with `node scripts/sync-tray-catalog.mjs`.
import { trayCatalogs, type TrayMessageKey } from "./tray-catalog.generated";

export type TrayPluralKey = TrayMessageKey extends infer Key extends string
	? Key extends `${infer Base}_${"one" | "other"}`
		? Base
		: never
	: never;

/** Main-process copy (the tray menu and the native agent dialogs) in one locale. */
export type TrayText = {
	t(key: TrayMessageKey, vars?: Record<string, string | number>): string;
	plural(base: TrayPluralKey, count: number): string;
};

function format(template: string, vars: Record<string, string | number>): string {
	return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) => String(vars[key] ?? match));
}

export function trayText(locale: AppLocale): TrayText {
	const catalog: Record<TrayMessageKey, string> = trayCatalogs[locale] ?? trayCatalogs.en;
	const t = (key: TrayMessageKey, vars?: Record<string, string | number>): string =>
		format(catalog[key] ?? key, { brand: PRODUCT_NAME, ...vars });
	return {
		t,
		plural: (base, count) => t((count === 1 ? `${base}_one` : `${base}_other`) as TrayMessageKey, { count }),
	};
}
