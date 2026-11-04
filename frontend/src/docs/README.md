# Docs site

Next.js static export (`output: "export"`), English search runs in the browser.

## Build

```sh
cd frontend/src/docs
npm ci
DOCS_URL=https://docs.tallpine.eu MARKETING_URL=https://tallpine.eu npm run build
```

`DOCS_URL` and `MARKETING_URL` (see `src/lib/constants.ts`) feed canonical URLs,
sitemap, robots.txt, llms.txt and JSON-LD. Unset, they keep the current defaults.
Build in place: OG images read the logo from `frontend/assets/tallpine/brand/tile.svg`
(skipped if the file is absent).

## Host requirements

- Publish `out/` (static files, served at the domain root; no base path).
- `out/_headers` (from `public/_headers`) is read by Cloudflare Pages and Netlify and
  serves `/api/search` as JSON. Other hosts must set that content type themselves,
  since the file has no extension.
- Directory URLs with trailing slashes (`trailingSlash: true`).
