// The OpenAPI types are generated into packages/shared/api/schema.ts (npm run
// api:ts) so the desktop and the phone read one file. This re-export keeps the
// existing `api/schema` import path working.
export type { components, operations, paths } from "../../../packages/shared/api/schema";
