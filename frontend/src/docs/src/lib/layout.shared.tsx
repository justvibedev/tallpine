import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";
import { COMPANY } from "./constants";

export function baseOptions(i18n: I18n, homeUrl = "/"): BaseLayoutProps {
  return {
    nav: {
      url: homeUrl,
      title: (
        <span className="inline-flex items-center gap-2 text-sm font-medium text-foreground">
          {/* The tile mark has no text, so the label carries the name. */}
          <img
            src="/tallpine-logo.svg"
            alt={COMPANY.NAME}
            width={20}
            height={20}
            className="size-5 shrink-0"
          />
          <span>{i18n._(msg`Tallpine Docs`)}</span>
        </span>
      ),
    },
    themeSwitch: {
      enabled: false,
    },
    links: [
      {
        text: i18n._(msg`Main site`),
        url: COMPANY.MARKETING_URL,
      },
      {
        text: "GitHub",
        url: COMPANY.GITHUB_URL,
      },
    ],
  };
}
