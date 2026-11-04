// Product identity shown in the app's own copy. Mirrors PRODUCT_NAME in
// frontend/src/shared/brand.ts; keep the two in step.
export const PRODUCT_NAME = "Tallpine";

/** Where the desktop shows the pairing QR and code. */
export const DESKTOP_PAIRING_PATH = `${PRODUCT_NAME} → Settings → Connections`;

/** Link schemes of older daemons whose stored messages must stay readable (`vo://sessions/...`). */
export const LEGACY_LINK_SCHEMES = ["ao", "vo"] as const;
/** Regex alternation matching every in-app session-link scheme. */
export const LINK_SCHEME_PATTERN = ["tallpine", ...LEGACY_LINK_SCHEMES].join("|");
/** Builds a regex from a source in which `@SCHEME@` stands for any in-app session-link scheme. */
export const linkRegExp = (source: string, flags?: string): RegExp => new RegExp(source.replaceAll("@SCHEME@", LINK_SCHEME_PATTERN), flags);
