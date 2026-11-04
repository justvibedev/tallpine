export const INSTALLER_DIRS: string[];
export const INSTALLER_FILES: string[];
export function buildInstallerManifest(repoRoot: string): { dirs: string[]; files: Record<string, string> };
export function writeInstallerManifest(repoRoot: string, out: string): void;
