import { usePathname, useRootNavigationState, useRouter } from "expo-router";
import { useEffect, useRef, useState } from "react";
import { launchRedirect } from "./onboarding";
import { loadOnboardingSkipped } from "./onboardingStore";
import { useApp } from "./store";

// Headless. Mounted beside PushManager in `app/_layout.tsx`; sends a first-run
// user to the welcome screen once both the saved config and the skip flag have
// resolved, and everyone else to Projects (the launch page). Decision logic lives in `onboarding.ts` so it is unit-testable.
export function OnboardingGate() {
	const router = useRouter();
	const pathname = usePathname();
	const navState = useRootNavigationState();
	const { config } = useApp();
	const [skipped, setSkipped] = useState<boolean | null>(null);
	// The gate redirects once per app launch. Without this, skipping would write
	// the flag but the intervening render — before the flag is re-read — would
	// bounce the user straight back to onboarding.
	const redirected = useRef(false);

	useEffect(() => {
		loadOnboardingSkipped().then(setSkipped);
	}, []);

	useEffect(() => {
		if (redirected.current) return;
		if (!navState?.key) return; // wait until navigation is ready to accept routes
		// `config` is null until the store's first load resolves; `launchRedirect`
		// treats that as "not known yet" and declines to act. It also leaves a
		// deep-linked launch (any pathname other than `/`) where it is.
		const configured = config === null ? null : config.host.trim().length > 0;
		const target = launchRedirect({ configured, skipped, pathname });
		if (target === null) {
			if (configured !== null && skipped !== null) redirected.current = true;
			return;
		}
		redirected.current = true;
		router.replace(target);
	}, [config, skipped, pathname, router, navState?.key]);

	return null;
}
