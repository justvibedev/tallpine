//go:build windows

package testenv

import (
	"encoding/binary"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CurrentUserSID returns a copy of the current process token's user SID.
func CurrentUserSID(t testing.TB) *windows.SID {
	t.Helper()
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid, err := user.User.Sid.Copy()
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

// WellKnownSID builds a well-known SID or fails the test.
func WellKnownSID(t testing.TB, kind windows.WELL_KNOWN_SID_TYPE) *windows.SID {
	t.Helper()
	sid, err := windows.CreateWellKnownSid(kind)
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

// RawDACL builds a one-ACE ACL of the given raw ACE type, so tests can feed
// layouts the Win32 builders refuse to create. Callers must
// runtime.KeepAlive the returned bytes while using the ACL.
func RawDACL(t testing.TB, aceType, aceFlags uint8, mask uint32, sid *windows.SID) ([]byte, *windows.ACL) {
	t.Helper()
	if sid == nil || !sid.IsValid() {
		t.Fatal("raw test ACL requires a valid SID")
	}
	extraPrefix := 0
	aclRevision := byte(2)
	switch aceType {
	case 0x04: // ACCESS_ALLOWED_COMPOUND_ACE_TYPE
		extraPrefix = 4
	case 0x05, 0x06, 0x0b, 0x0c: // object ACE layouts
		extraPrefix = 4
		aclRevision = 4
	}
	aceSize := 8 + extraPrefix + sid.Len()
	aclSize := 8 + aceSize
	raw := make([]byte, aclSize)
	raw[0] = aclRevision
	binary.LittleEndian.PutUint16(raw[2:4], uint16(aclSize))
	binary.LittleEndian.PutUint16(raw[4:6], 1)
	raw[8] = aceType
	raw[9] = aceFlags
	binary.LittleEndian.PutUint16(raw[10:12], uint16(aceSize))
	binary.LittleEndian.PutUint32(raw[12:16], mask)
	if aceType == 0x04 {
		binary.LittleEndian.PutUint16(raw[16:18], 1) // COMPOUND_ACE_IMPERSONATION
	}
	sidOffset := 16 + extraPrefix
	copy(raw[sidOffset:], unsafe.Slice((*byte)(unsafe.Pointer(sid)), sid.Len()))
	return raw, (*windows.ACL)(unsafe.Pointer(&raw[0]))
}
