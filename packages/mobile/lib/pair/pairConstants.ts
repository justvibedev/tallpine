
export const RECONNECT_WAIT_MS = 8000;
export const LOAD_ATTEMPTS = 2;
export const LOAD_RETRY_MS = 1500;
export const LEAVE_CHECK_MS = 1500;
export const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
export const CORNER = 26;
export const CORNER_W = 3;
