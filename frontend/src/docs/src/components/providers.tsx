"use client";

import { setupI18n } from "@lingui/core";
import { I18nProvider } from "@lingui/react";
import { RootProvider } from "fumadocs-ui/provider/next";
import { type ReactNode, useMemo } from "react";
import { CATALOGS } from "@/i18n/catalogs";
import { DEFAULT_LOCALE } from "@/i18n/locales";
import { frameworkTranslations } from "@/i18n/ui-messages";
import { DocsSearchDialog } from "./search-dialog";

/** Docs are English only; the Lingui catalog still supplies the interface strings. */
export function Providers({ children }: { children: ReactNode }) {
  const i18n = useMemo(
    () => setupI18n({ locale: DEFAULT_LOCALE, messages: { [DEFAULT_LOCALE]: CATALOGS[DEFAULT_LOCALE] } }),
    [],
  );
  const frameworkStrings = useMemo(() => frameworkTranslations(i18n), [i18n]);

  return (
    <I18nProvider i18n={i18n}>
      <RootProvider
        theme={{ enabled: false }}
        search={{ SearchDialog: DocsSearchDialog }}
        i18n={{ locale: DEFAULT_LOCALE, translations: frameworkStrings }}
      >
        {children}
      </RootProvider>
    </I18nProvider>
  );
}
