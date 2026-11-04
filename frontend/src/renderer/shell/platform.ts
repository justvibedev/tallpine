import {
	isLinuxPlatform,
	isMacPlatform,
	isWindowsPlatform,
	usesFramedAppTopbar,
	hidesShellTopbar,
} from "../lib/platform";

export const isMac = isMacPlatform();

export const isWindows = isWindowsPlatform();

export const isLinux = isLinuxPlatform();

export const framedAppTopbar = usesFramedAppTopbar();

export const shellTopbarHiddenByPlatform = hidesShellTopbar();
