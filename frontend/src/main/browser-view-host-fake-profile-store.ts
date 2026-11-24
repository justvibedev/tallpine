import { vi } from "vitest";
import { browserProfilePartition, type BrowserProfile } from "../shared/browser-profiles";
import type { BrowserProfileStore } from "./browser-profile-store";

export function fakeBrowserProfileStore(
	profile: BrowserProfile,
	bindings: Record<string, string | null>,
	defaultProfileId: string | null = null,
): BrowserProfileStore {
	return {
		profiles: [profile],
		getProfile: (profileId: string) => (profileId === profile.id ? { ...profile } : undefined),
		getSessionProfileId: (sessionId: string) => bindings[sessionId],
		getDefaultProfileId: () => defaultProfileId,
		// A binding of `null` is an explicit, durable choice of Temporary and
		// must be kept distinct from an absent key (no binding at all).
		bindSession: vi.fn(async (sessionId: string, profileId: string | null) => {
			bindings[sessionId] = profileId;
		}),
		isProfileOperationInProgress: () => false,
		waitForProfileOperation: async () => undefined,
		partitionForProfile: (profileId: string) => browserProfilePartition(profileId),
	} as unknown as BrowserProfileStore;
}
