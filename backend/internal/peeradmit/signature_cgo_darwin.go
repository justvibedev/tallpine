//go:build darwin && cgo

//nolint:gocritic // cgo translation false positives
package peeradmit

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>

// checkAuditToken returns errSecSuccess when the process named by the audit
// token satisfies the requirement string. It never reads the process from a
// path or pid, so the process cannot be swapped under the check.
static OSStatus checkAuditToken(const unsigned int *token, const char *requirement) {
	CFDataRef data = CFDataCreate(NULL, (const UInt8 *)token, 8 * sizeof(unsigned int));
	if (!data) return errSecAllocate;
	const void *keys[] = { kSecGuestAttributeAudit };
	const void *values[] = { data };
	CFDictionaryRef attributes = CFDictionaryCreate(NULL, keys, values, 1,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFRelease(data);
	if (!attributes) return errSecAllocate;
	SecCodeRef code = NULL;
	OSStatus status = SecCodeCopyGuestWithAttributes(NULL, attributes, kSecCSDefaultFlags, &code);
	CFRelease(attributes);
	if (status != errSecSuccess) return status;
	CFStringRef text = CFStringCreateWithCString(NULL, requirement, kCFStringEncodingUTF8);
	if (!text) { CFRelease(code); return errSecAllocate; }
	SecRequirementRef parsed = NULL;
	status = SecRequirementCreateWithString(text, kSecCSDefaultFlags, &parsed);
	CFRelease(text);
	if (status != errSecSuccess) { CFRelease(code); return status; }
	status = SecCodeCheckValidity(code, kSecCSDefaultFlags, parsed);
	CFRelease(parsed);
	CFRelease(code);
	return status;
}
*/
import "C"

import (
	"unsafe"

	"tallpine.eu/tallpine/backend/internal/releasesigner"
)

// osStatusNotSatisfied is errSecCSReqFailed: the code is valid but is someone
// else's. Any other failure means the check could not be made.
const osStatusNotSatisfied = -67050

// desktopVerifier checks a peer against our Team ID and the app's identifier.
type desktopVerifier struct{ requirement string }

// SignatureCheckBuilt says whether this build can verify a code signature.
const SignatureCheckBuilt = true

// NewDesktopVerifier returns the real signature check; the Team ID has already
// passed DefaultMode's pattern, so it cannot alter the requirement's meaning.
func NewDesktopVerifier(teamID, identifier string) SignatureVerifier {
	return desktopVerifier{requirement: `identifier "` + identifier + `" and ` + releasesigner.TeamRequirement(teamID)}
}

// Verify checks the process the audit token names.
func (v desktopVerifier) Verify(token [8]uint32) Signature {
	requirement := C.CString(v.requirement)
	defer C.free(unsafe.Pointer(requirement))
	switch C.checkAuditToken((*C.uint)(unsafe.Pointer(&token[0])), requirement) {
	case C.errSecSuccess:
		return SignatureDesktop
	case osStatusNotSatisfied:
		return SignatureOther
	}
	return SignatureUnknown
}
