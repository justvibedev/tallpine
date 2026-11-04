import { applyHermeticTestEnv } from "./hermetic-env";

// Minimal setup for the node-environment project: no DOM, no Testing Library,
// no i18n catalogs. The jsdom setup (setup.ts) imports this file first.
applyHermeticTestEnv();
