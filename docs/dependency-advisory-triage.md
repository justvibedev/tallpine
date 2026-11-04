# Dependency advisory triage

Scanner counts are not exploits. This records the OSV matches that remain after `scripts/security-scan` (osv-scanner, 2026-11-02: 0 critical, 0 high, 10 medium/unknown) and why none needs a change now. Re-run the scan and revisit when a fix lands in the upstream parent.

| Advisory | Package | Where | Reachable? | Decision |
| --- | --- | --- | --- | --- |
| GO-2026-5932 (`x/crypto/openpgp` unmaintained) | `golang.org/x/crypto` 0.58.0 | `backend/go.mod`, indirect | No. No Go file imports `x/crypto/openpgp`, and no fixed version exists (affected range starts at 0). | Not applicable. Move on only if a direct import of openpgp is ever added. |
| GHSA-g3hv-75wg-m249 (minimatch globparts DoS, fixed in 11.0.1) | `minimatch` 3.x, 5.x, 9.x, 10.x | `frontend/`, root, `packages/mobile`, `frontend/src/docs` lockfiles | No. All copies are transitive dependencies of build, packaging and test tooling (electron-builder, asar, eslint, Expo, the docs generator). They glob repo-controlled patterns at build time. The attack needs an attacker-controlled pattern and `optimizationLevel >= 2`; no shipped runtime code passes user input to minimatch. | Not applicable. The fix is a major bump (11.x) of a transitive package that its parents pin to older majors (`^3`, `^5`, `^9`, `^10`); forcing overrides risks breaking those tools for no runtime gain. |
| GHSA-3g3m-2fjr-6pw4 (glob AbortSignal keeps walking, fixed in 14.0.2) | `glob` 13.0.6 | `frontend/`, `packages/mobile` (dev) | No. Pulled in by Expo and Electron build tooling; none of it uses `AbortSignal` to bound a walk on untrusted input, and none ships in the app. | Not applicable. Wait for the parent (Expo / electron-builder) to adopt glob 14. |

Impact if wrong: a build-time CPU or I/O stall on a developer machine or CI, not a runtime or data-exposure issue.
