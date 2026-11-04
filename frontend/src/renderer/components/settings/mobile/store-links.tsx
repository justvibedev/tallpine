/* Split out of ConnectMobileContent.tsx (B298, P55). */

import { MOBILE_ANDROID_STORE_URL, MOBILE_IOS_STORE_URL } from "../../../../shared/brand";

// The pairing panel is a fixed 240px square (size-60) so the card stays short
// and a code change can never move the layout. Codes are drawn at that size.
export const QR_CODE_SIZE = 240;

export const STORE_QR_SIZE = 140;

// Only listings that exist: Tallpine has none yet, and the official AO apps
// cannot pair with it, so "Get the app" stays hidden until one is published.
export const STORE_LINKS = ([
	{
		key: "ios",
		Icon: AppleIcon,
		url: MOBILE_IOS_STORE_URL,
		labelKey: "mobile.ios",
		ariaKey: "mobile.iosStoreAria",
		testId: "ios-store-qr",
	},
	{
		key: "android",
		Icon: AndroidIcon,
		url: MOBILE_ANDROID_STORE_URL,
		labelKey: "mobile.android",
		ariaKey: "mobile.androidSignupAria",
		testId: "android-play-qr",
	},
] as const).flatMap((link) => (link.url ? [{ ...link, url: link.url }] : []));

export function AppleIcon({ className }: { className?: string }) {
	return (
		<svg aria-hidden="true" className={className} fill="currentColor" viewBox="0 0 384 512">
			<path d="M318.7 268.7c-.2-36.7 16.4-64.4 50-84.8-18.8-26.9-47.2-41.7-84.7-44.6-35.5-2.8-74.3 20.7-88.5 20.7-15 0-49.4-19.7-76.4-19.7C63.3 141.2 4 184.8 4 273.5q0 39.3 14.4 81.2c12.8 36.7 59 126.7 107.2 125.2 25.2-.6 43-17.9 75.8-17.9 31.8 0 48.3 17.9 76.4 17.9 48.6-.7 90.4-82.5 102.6-119.3-65.2-30.7-61.7-90-61.7-91.9zm-56.6-164.2c27.3-32.4 24.8-61.9 24-72.5-24.1 1.4-52 16.4-67.9 34.9-17.5 19.8-27.8 44.3-25.6 71.9 26.1 2 49.9-11.4 69.5-34.3Z" />
		</svg>
	);
}

export function AndroidIcon({ className }: { className?: string }) {
	return (
		<svg aria-hidden="true" className={className} fill="currentColor" viewBox="0 0 576 512">
			<path d="M420.55 301.93a24 24 0 1 1 24-24 24 24 0 0 1-24 24m-265.1 0a24 24 0 1 1 24-24 24 24 0 0 1-24 24m273.7-144.48 47.94-83a10 10 0 1 0-17.27-10l-48.54 84.07a301.25 301.25 0 0 0-246.56 0L116.18 64.45a10 10 0 1 0-17.27 10l47.94 83C64.53 202.22 8.24 285.55 0 384h576c-8.24-98.45-64.54-181.78-146.85-226.55" />
		</svg>
	);
}

/** Quiet store link: platform icon, name and an outbound arrow, muted until
 *  hovered. The icons carry the "this is a link" cue, not an underline. */
export const STORE_LINK_CLASS =
	"inline-flex items-center gap-1 rounded-sm text-settings-muted transition-colors duration-(--duration-fast) hover:text-settings-label focus-visible:text-settings-label focus-visible:outline-none";

/** Icon actions beside the password. */
export const ICON_ACTION_CLASS =
	"inline-flex size-6 items-center justify-center rounded-sm text-settings-muted transition-colors duration-(--duration-fast) hover:text-settings-label disabled:opacity-50";
