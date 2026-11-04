import type { WebContents } from "electron";
import type { AppShortcutId, ShortcutChord } from "../../shared/shortcuts";

type BrowserShortcutInput = {
  key: string;
  control: boolean;
  meta: boolean;
  shift: boolean;
  alt: boolean;
  type: string;
  isAutoRepeat?: boolean;
};

export type BrowserShortcutAction =
  | "new-tab"
  | "reopen-tab"
  | "close-tab"
  | "focus-location"
  | "find"
  | "reload"
  | "back"
  | "forward";

export function browserShortcutAction(
  input: BrowserShortcutInput,
  isMac: boolean,
): BrowserShortcutAction | null {
  // Page history, not Tallpine's route history: ⌘[ / ⌘] on macOS, Alt+←/→ elsewhere.
  if (!input.shift) {
    if (isMac && input.meta && !input.control && !input.alt) {
      if (input.key === "[") return "back";
      if (input.key === "]") return "forward";
    }
    if (!isMac && input.alt && !input.control && !input.meta) {
      if (input.key === "ArrowLeft") return "back";
      if (input.key === "ArrowRight") return "forward";
    }
  }
  const primaryModifier = isMac
    ? input.meta && !input.control
    : input.control && !input.meta;
  if (primaryModifier && !input.alt) {
    if (input.shift)
      return input.key.toLowerCase() === "t" ? "reopen-tab" : null;
    switch (input.key.toLowerCase()) {
      case "t":
        return "new-tab";
      case "w":
        return "close-tab";
      case "l":
        return "focus-location";
      case "f":
        return "find";
      case "r":
        return "reload";
    }
  }
  return null;
}

export function shouldHandleAppShortcutInBrowserContext(
  id: AppShortcutId,
  chord: ShortcutChord,
  isMac: boolean,
): boolean {
  if (id === "new-shell-terminal" || id === "close-shell-terminal")
    return false;
  return (
    browserShortcutAction(
      {
        key: chord.key,
        control: chord.ctrl,
        meta: chord.meta,
        shift: chord.shift,
        alt: chord.alt,
        type: "keyDown",
      },
      isMac,
    ) === null
  );
}

export function canWriteBrowserClipboard(
  webContents: Pick<WebContents, "getURL"> | null,
  permission: string,
  requestingUrl: string | undefined,
  isMainFrame: boolean,
): boolean {
  if (
    permission !== "clipboard-sanitized-write" ||
    !webContents ||
    !requestingUrl ||
    !isMainFrame
  )
    return false;
  try {
    const activeUrl = new URL(webContents.getURL());
    const requestedUrl = new URL(requestingUrl);
    return (
      (requestedUrl.protocol === "http:" ||
        requestedUrl.protocol === "https:") &&
      requestedUrl.origin === activeUrl.origin
    );
  } catch {
    return false;
  }
}

export function isHardReloadShortcut(input: BrowserShortcutInput, isMac: boolean): boolean {
  const primary = isMac ? input.meta && !input.control : input.control && !input.meta;
  return primary && input.shift && !input.alt && input.key.toLowerCase() === "r";
}
