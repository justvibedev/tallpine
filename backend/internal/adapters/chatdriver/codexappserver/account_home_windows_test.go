//go:build windows

package codexappserver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"

	"golang.org/x/sys/windows"
)

func TestManagedHomeDACLRejectsUnsupportedEffectiveAllowACEs(t *testing.T) {
	user := testenv.CurrentUserSID(t)
	system := testenv.WellKnownSID(t, windows.WinLocalSystemSid)
	administrators := testenv.WellKnownSID(t, windows.WinBuiltinAdministratorsSid)
	untrusted := testenv.WellKnownSID(t, windows.WinBuiltinUsersSid)
	for name, aceType := range map[string]uint8{
		"compound allow":        0x04,
		"object allow":          0x05,
		"callback allow":        0x09,
		"callback object allow": 0x0b,
	} {
		t.Run(name, func(t *testing.T) {
			raw, dacl := testenv.RawDACL(t, aceType, 0, uint32(windows.GENERIC_READ), untrusted)
			private := managedHomeDACLPrivate(dacl, user, system, administrators)
			runtime.KeepAlive(raw)
			if private {
				t.Fatalf("effective ACE type %#x was accepted", aceType)
			}
		})
	}
}

func TestManagedHomeDACLKeepsKnownDeniesAndInheritOnlyNonGranting(t *testing.T) {
	user := testenv.CurrentUserSID(t)
	system := testenv.WellKnownSID(t, windows.WinLocalSystemSid)
	administrators := testenv.WellKnownSID(t, windows.WinBuiltinAdministratorsSid)
	untrusted := testenv.WellKnownSID(t, windows.WinBuiltinUsersSid)
	for name, tc := range map[string]struct {
		aceType uint8
		flags   uint8
	}{
		"simple deny":                 {aceType: windows.ACCESS_DENIED_ACE_TYPE},
		"object deny":                 {aceType: accountHomeAccessDeniedObjectACEType},
		"callback deny":               {aceType: accountHomeAccessDeniedCallbackACEType},
		"callback object deny":        {aceType: accountHomeAccessDeniedCallbackObjectACEType},
		"inherit-only compound allow": {aceType: 0x04, flags: windows.INHERIT_ONLY_ACE},
	} {
		t.Run(name, func(t *testing.T) {
			raw, dacl := testenv.RawDACL(t, tc.aceType, tc.flags, uint32(windows.GENERIC_ALL), untrusted)
			private := managedHomeDACLPrivate(dacl, user, system, administrators)
			runtime.KeepAlive(raw)
			if !private {
				t.Fatalf("non-granting ACE type %#x was rejected", tc.aceType)
			}
		})
	}
}

func protectManagedHomeForTest(t *testing.T, path string) {
	t.Helper()
	applyOwnerOnlyDACL(t, path)
}

// TestManagedHomePrivateOnWindows pins the Windows meaning of "private managed
// home". os.Mkdir(path, 0o700) reports mode 0777 back through Lstat on Windows,
// so a POSIX permission comparison can never accept a directory Tallpine just
// created; the owner-only protected DACL is what actually makes it private.
func TestManagedHomePrivateOnWindows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode().Perm() == 0o700 {
		t.Fatal("Windows reported POSIX 0700 on a directory; this test no longer covers the bug it was written for")
	}
	if managedHomePrivate(dir, info) {
		t.Fatal("inherited-DACL directory accepted as a private managed home")
	}

	applyOwnerOnlyDACL(t, dir)
	info, err = os.Lstat(dir)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if !managedHomePrivate(dir, info) {
		t.Fatal("owner-only protected DACL rejected as a private managed home")
	}
}

// applyOwnerOnlyDACL mirrors the protection Tallpine applies when it creates a
// managed credential home.
func applyOwnerOnlyDACL(t *testing.T, path string) {
	t.Helper()
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatalf("open token: %v", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatalf("token user: %v", err)
	}
	entry := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}
	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, nil)
	if err != nil {
		t.Fatalf("build DACL: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		t.Fatalf("set DACL: %v", err)
	}
}
