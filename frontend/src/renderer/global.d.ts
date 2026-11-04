import type { TallpineBridge } from "../preload";

declare global {
	interface Window {
		tallpine?: TallpineBridge;
	}

	interface ImportMetaEnv {
		readonly VITE_TALLPINE_GIT_BRANCH?: string;
		readonly VITE_TALLPINE_GIT_COMMIT?: string;
		readonly VITE_TALLPINE_GIT_WORKTREE?: string;
		readonly VITE_TALLPINE_GIT_DIRTY?: string;
	}
}

export {};
