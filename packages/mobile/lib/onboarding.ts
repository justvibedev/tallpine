// Pure decision logic for first-run onboarding. Deliberately free of React
// Native / Expo imports so it can be unit-tested directly (mirroring
// `pushStatus.ts`); the AsyncStorage side lives in `onboardingStore.ts`.

/** Persisted flag: the user dismissed onboarding without pairing. */
export const ONBOARDING_SKIPPED_KEY = "ao.onboardingSkipped";

export type OnboardingInput = {
	// Whether a server is configured. `null` means "not loaded yet".
	configured: boolean | null;
	// Whether the user has dismissed onboarding. `null` means "not loaded yet".
	skipped: boolean | null;
};

/**
 * Should the app take the user to onboarding?
 *
 * Both inputs are read asynchronously (config from AsyncStorage + SecureStore,
 * the flag from AsyncStorage), so either can still be `null` on the first
 * render. Redirecting on incomplete state would yank a paired user onto the
 * welcome screen for a frame on every cold start, so unknown means "do nothing
 * yet" rather than "assume false".
 */
export function shouldOnboard({ configured, skipped }: OnboardingInput): boolean {
	if (configured === null || skipped === null) return false;
	return !configured && !skipped;
}

/** The page a cold launch lands on once the app knows whether to onboard. */
export const LAUNCH_ROUTE = "/projects";

/**
 * Where the launch should send the user, or `null` to stay put.
 *
 * Every cold start opens at `/` (the Workers tab). Once both inputs are known,
 * a first-run user goes to onboarding and everyone else to Projects. Any other
 * pathname means the user (or a deep link) already chose a page.
 */
export function launchRedirect(input: OnboardingInput & { pathname: string }): "/onboarding" | typeof LAUNCH_ROUTE | null {
	if (input.configured === null || input.skipped === null) return null;
	if (input.pathname !== "/") return null;
	return shouldOnboard(input) ? "/onboarding" : LAUNCH_ROUTE;
}
