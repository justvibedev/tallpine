import { contextBridge } from "electron";
import { installChromeObject } from "./shared/browser-chrome-object";

// Part of the Browser tab preload (annotate-preload.ts). Google sign-in reads
// window.chrome before any of its own scripts finish, so the members are
// added in the main world before the page runs.
try {
	contextBridge.executeInMainWorld({ func: installChromeObject });
} catch {
	// A page without a main world to reach (e.g. a blocked frame) keeps working.
}
