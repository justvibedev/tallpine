import { contextBridge, ipcRenderer } from "electron";
import { BROWSER_WEBAUTHN_STATE_CHANNEL, installPasskeyRequestWatch } from "./shared/browser-passkey-watch";

// Part of the Browser tab preload (annotate-preload.ts). The watch has to wrap
// the page's own navigator.credentials, so it runs in the main world before
// any page script.
try {
	contextBridge.executeInMainWorld({
		func: installPasskeyRequestWatch,
		args: [(pending: boolean) => ipcRenderer.send(BROWSER_WEBAUTHN_STATE_CHANNEL, pending === true)],
	});
} catch {
	// A page without a main world to reach (e.g. a blocked frame) keeps working.
}
