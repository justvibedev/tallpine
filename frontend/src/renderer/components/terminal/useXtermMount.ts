import { useEffect } from "react";
import type { XtermTerminalProps } from "./xterm-types";
import type { useXtermState } from "./useXtermState";


import type { XtermEnv, XtermState } from "./xterm-types";
import { installSetup } from "./xterm-setup";
import { installScrollbar } from "./xterm-scrollbar";
import { installInput } from "./xterm-input";
import { installFit } from "./xterm-fit";
import { installActivation } from "./xterm-activation";
import { installCleanup } from "./xterm-cleanup";

export function useXtermMount(props: XtermTerminalProps, state: ReturnType<typeof useXtermState>) {
	useEffect(() => {
		const env: XtermEnv = { ...state, props };
		const st = {} as XtermState;
		const setup = installSetup(env, st);
		if (!setup) return undefined;
		const scrollbar = installScrollbar(env, st, setup);
		const input = installInput(env, st, setup);
		const fit = installFit(env, st, setup, input);
		const activation = installActivation(env, st, setup, scrollbar, input, fit);
		return installCleanup(env, st, setup, scrollbar, input, fit, activation);
	}, []);
}
