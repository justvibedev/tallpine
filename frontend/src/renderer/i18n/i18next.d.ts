import "i18next";
import type { DefaultNamespace, Namespace, TOptions } from "i18next";
import en from "./en.json";
import type { MessageKey, PluralMessageKey } from "./messages";

/** Every key `t()` accepts: catalog keys plus the bases of plural keys. */
type CatalogKey = MessageKey | PluralMessageKey;
type CatalogTOptions = TOptions & Readonly<Record<string, unknown>>;
/** `returnObjects: true` asks for a catalog object, not a string. */
type CatalogTReturn<Options> = Options extends { returnObjects: true } ? unknown : string;

declare module "i18next" {
	interface CustomTypeOptions {
		defaultNS: "translation";
		returnNull: false;
		keySeparator: false;
		resources: {
			translation: typeof en;
		};
	}

	// Typecheck cost guard. i18next's own signature checks a key by resolving
	// return and interpolation types over all ~3,250 catalog keys (ParseKeys,
	// InterpolationMap). That buys nothing here: en.json values all type as
	// `string`, so there are no placeholder or return types to infer, yet it was
	// ~40% of the frontend typecheck. These signatures, tried first, keep the
	// same key checking (and editor completion) with a plain membership test:
	//  1. a catalog key (or plural base); `unknown` for `returnObjects: true`;
	//  2-3. a key missing from en.json, only with a string default value.
	// Anything else (a typo without a default, a key prefix) falls through to
	// i18next's signature, which reports the error. `<Trans>` has the same cost:
	// use `CatalogTrans` from ../i18n.
	interface TFunction<Ns extends Namespace = DefaultNamespace, KPrefix = undefined> {
		<const Options extends CatalogTOptions = {}>(
			key: KPrefix extends undefined ? CatalogKey | CatalogKey[] : never,
			options?: Options,
		): CatalogTReturn<Options>;
		<const Key extends string>(
			key: KPrefix extends undefined ? (Key extends CatalogKey ? never : Key) : never,
			options: CatalogTOptions & { defaultValue: string },
		): string;
		<const Key extends string>(
			key: KPrefix extends undefined ? (Key extends CatalogKey ? never : Key) : never,
			defaultValue: string,
			options?: CatalogTOptions,
		): string;
	}
}
