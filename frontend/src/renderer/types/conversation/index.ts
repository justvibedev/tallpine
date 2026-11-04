/**
 * Chat conversation view model.
 *
 * These mirror `backend/internal/domain/conversation.go` field for field. The
 * endpoint transport is generated from OpenAPI, while this file keeps the
 * renderer's discriminated semantic view of timeline details. Keep the two in
 * sync when the daemon adds a conversation shape.
 *
 * The renderer is thin on purpose: ordering, capability, permission, and
 * lifecycle decisions all belong to the daemon. Nothing here recomputes them.
 */


export * from "./turn";
export * from "./details";
export * from "./settings";
export * from "./selectors";
