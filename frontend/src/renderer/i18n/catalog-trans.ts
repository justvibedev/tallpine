import type { TFunction } from "i18next";
import type { ComponentType, ReactElement } from "react";
import { Trans } from "react-i18next";
import type { MessageKey } from "./messages";

export interface CatalogTransProps {
	i18nKey: MessageKey;
	values?: Readonly<Record<string, unknown>>;
	components?: Readonly<Record<string, ReactElement>>;
	t?: TFunction;
}

/**
 * react-i18next's `Trans`, keyed by catalog key. `Trans` types `i18nKey`
 * through i18next's ParseKeys and InterpolationMap over every catalog key,
 * which cost the frontend typecheck as much as all the `t()` calls together
 * (see i18next.d.ts). This is the same component with the same key check as a
 * plain union, so a typo still fails to compile.
 */
export const CatalogTrans = Trans as unknown as ComponentType<CatalogTransProps>;
