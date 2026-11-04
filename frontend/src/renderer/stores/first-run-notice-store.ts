import { create } from "zustand";
import { tallpineBridge } from "../lib/bridge";

type FirstRunNoticeState = {
	/** Settings have been read; the notice never flashes before this. */
	loaded: boolean;
	accepted: boolean;
	load: () => Promise<void>;
	accept: () => Promise<void>;
};

export const useFirstRunNoticeStore = create<FirstRunNoticeState>((set, get) => ({
	loaded: false,
	accepted: false,
	load: async () => {
		if (get().loaded) return;
		try {
			const settings = await tallpineBridge.uiSettings.get();
			set({ accepted: settings.firstRunNoticeAccepted, loaded: true });
		} catch {
			// An unreadable setting must not block the app, and must not nag: stay hidden.
			set({ accepted: true, loaded: true });
		}
	},
	accept: async () => {
		set({ accepted: true });
		try {
			await tallpineBridge.uiSettings.set({ firstRunNoticeAccepted: true });
		} catch {
			// Not persisted: the notice shows again next launch, which is the safe direction.
		}
	},
}));
