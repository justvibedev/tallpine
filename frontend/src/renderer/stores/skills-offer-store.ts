import { create } from "zustand";

type SkillsOfferState = {
	/** `${projectId}:${sessionId}` keys already offered this run: one per conversation start. */
	shown: Set<string>;
	/** Keys whose card is on screen (shown and not dismissed). */
	active: Set<string>;
	/** The project whose review dialog is open, opened from the chat card or the project-add toast. */
	reviewing: string | null;
	/** Returns true when a card should appear for this session now, and records it. */
	claim: (projectId: string, sessionId: string) => boolean;
	dismiss: (projectId: string, sessionId: string) => void;
	openReview: (projectId: string) => void;
	closeReview: () => void;
};

export const skillsOfferKey = (projectId: string, sessionId: string) => `${projectId}:${sessionId}`;

export const useSkillsOfferStore = create<SkillsOfferState>((set, get) => ({
	shown: new Set(),
	active: new Set(),
	reviewing: null,
	claim: (projectId, sessionId) => {
		const key = skillsOfferKey(projectId, sessionId);
		if (get().shown.has(key)) return false;
		set((s) => ({ shown: new Set(s.shown).add(key), active: new Set(s.active).add(key) }));
		return true;
	},
	dismiss: (projectId, sessionId) =>
		set((s) => {
			const active = new Set(s.active);
			active.delete(skillsOfferKey(projectId, sessionId));
			return { active };
		}),
	openReview: (projectId) => set({ reviewing: projectId }),
	closeReview: () => set({ reviewing: null }),
}));
