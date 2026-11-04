

// Tab readiness bounds the initial about:blank load every tab performs: CDP
// commands await it, so a load that never settles would wedge the session's
// entire command queue (#5369 wedge family). The default sits above the 55s
// wait-action ceiling, so only a genuinely stuck load trips it.
export const DEFAULT_ENTRY_READY_TIMEOUT_MS = 60_000;

export const loadEntryReady = (
  view: { webContents: { loadURL: (url: string) => Promise<void> } },
  timeoutMs: number,
  onLateSuccess?: () => void,
): Promise<void> => {
  const ready = view.webContents.loadURL("about:blank");
  // Keep an unobserved tab initialization failure from becoming an unhandled
  // rejection; callers that need the target still await the raced promise.
  void ready.catch(() => undefined);
  let timedOut = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const deadline = new Promise<never>((_, reject) => {
    timer = setTimeout(() => {
      timedOut = true;
      reject(
        Object.assign(new Error("Browser tab initialization timed out"), {
          code: "BROWSER_DEVTOOLS_UNAVAILABLE",
        }),
      );
    }, timeoutMs);
  });
  // A load that finishes after the timeout already rejected readiness would
  // otherwise leave entry.ready permanently rejected, so every later navigation
  // and command keeps failing until the tab is recreated (#5369 follow-up). Let
  // a late success restore readiness via the caller-supplied repair.
  void ready.then(
    () => {
      if (timedOut) onLateSuccess?.();
    },
    () => undefined,
  );
  return Promise.race([
    ready.finally(() => {
      if (timer !== undefined) clearTimeout(timer);
    }),
    deadline,
  ]);
};
