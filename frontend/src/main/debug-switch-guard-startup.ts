import { app } from "electron";
import { enforceDebugSwitchPolicy } from "./debug-switch-guard";

// Imported first by app-bootstrap.ts (itself main.ts's first import), so it evaluates before any other module.
enforceDebugSwitchPolicy({
	isPackaged: app.isPackaged,
	argv: process.argv,
	env: process.env,
	hasSwitch: (name) => app.commandLine.hasSwitch(name),
	exit: (code) => {
		app.exit(code);
		process.exit(code); // app.exit can return before the loop stops; keep later imports from running
	},
	log: (message) => console.error(message),
});
