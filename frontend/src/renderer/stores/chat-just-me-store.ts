import { create } from "zustand";

/** localStorage lives under ~/.tallpine/electron, where main.ts pins userData. */
export const justMeStorageKey = "tallpine.chat.justMe";

function storage(): Storage | null {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

type ChatJustMeState = {
	/** Chat shows only the owner's messages and the replies to them; the choice persists. */
	justMe: boolean;
	toggleJustMe: () => void;
};

export const useChatJustMeStore = create<ChatJustMeState>((set) => ({
	justMe: storage()?.getItem(justMeStorageKey) === "true",
	toggleJustMe: () =>
		set((state) => {
			const justMe = !state.justMe;
			storage()?.setItem(justMeStorageKey, String(justMe));
			return { justMe };
		}),
}));
