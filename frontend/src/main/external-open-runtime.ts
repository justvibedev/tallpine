import { shell } from "electron";
import { daemonAuth } from "./app-runtime-state";
import { probeDaemonTcp } from "./daemon-tcp-probe";
import { type DaemonUrlGate, openGatedExternalURL } from "./external-open";

const daemonUrlGate: DaemonUrlGate = { port: () => daemonAuth.port(), tcpOpen: probeDaemonTcp };

/** Every main-process hand-off of a URL to the system browser goes through here. */
export function openInSystemBrowser(url: string): Promise<void> {
	return openGatedExternalURL(url, shell, daemonUrlGate);
}
