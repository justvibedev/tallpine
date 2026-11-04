import type { Messages } from "@lingui/core";
import { messages as en } from "../../locales/en/messages";
import type { SupportedLocale } from "./locales";

// The interface catalog is a few dozen strings, so it ships with the page.
export const CATALOGS: Record<SupportedLocale, Messages> = {
  en,
};
