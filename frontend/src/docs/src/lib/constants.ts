// Override at build time (e.g. DOCS_URL=https://docs.example.com npm run build).
// next.config.mjs inlines both into client bundles, which only works for literal
// `process.env.NAME` reads, so don't make these lookups dynamic.
// Trailing slashes are dropped so `${DOCS_URL}/path` stays well formed.
const clean = (value: string | undefined, fallback: string) => (value?.trim() || fallback).replace(/\/+$/, "");

export const COMPANY = {
  NAME: "Tallpine",
  MARKETING_URL: clean(process.env.MARKETING_URL, "https://tallpine.eu"),
  DOCS_URL: clean(process.env.DOCS_URL, "https://docs.tallpine.eu"),
  GITHUB_URL: "https://github.com/justvibedev/tallpine",
} as const;
