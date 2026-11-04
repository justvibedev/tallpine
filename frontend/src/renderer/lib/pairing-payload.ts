/**
 * Building the pairing code the mobile app scans.
 *
 * Pure, and deliberately not inside the modal: it is the wire contract with the
 * phone, and the phone's parser is tested against the same shape in
 * packages/mobile/lib/pairingCode.ts.
 */

// The pairing code is a wire contract with the phone, so the builder and
// codec live in @tallpine/shared and both apps import the same one.
export { buildPairingOffer, pairingCodeUrl } from "@tallpine/shared/pairing/offer";
export type { PairingEndpoint, PairingOffer, PairingOfferInput } from "@tallpine/shared/pairing/offer";
