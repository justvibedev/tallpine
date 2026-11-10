//go:build !(darwin && cgo)

package peeradmit

// SignatureCheckBuilt says whether this build can verify a code signature.
const SignatureCheckBuilt = false

// unavailableVerifier is used where there is no Security.framework.
type unavailableVerifier struct{}

// NewDesktopVerifier returns a check that always reports unknown.
func NewDesktopVerifier(string, string) SignatureVerifier { return unavailableVerifier{} }

// Verify reports that no signature could be checked.
func (unavailableVerifier) Verify([8]uint32) Signature { return SignatureUnknown }
