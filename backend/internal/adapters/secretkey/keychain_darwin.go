//go:build darwin && cgo

// gocritic reads the cgo-translated source and reports positions that do not
// exist in this file (dupImport of "C", phantom uncheckedInlineErr).
//
//nolint:gocritic // cgo translation false positives, see above
package secretkey

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>

static CFStringRef tp_str(const char *s) {
	return CFStringCreateWithCString(kCFAllocatorDefault, s, kCFStringEncodingUTF8);
}

// tp_query builds the item query; keychain may be NULL (default search list).
static CFMutableDictionaryRef tp_query(const char *service, const char *account, SecKeychainRef keychain) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFStringRef svc = tp_str(service), acct = tp_str(account);
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecAttrService, svc);
	CFDictionarySetValue(q, kSecAttrAccount, acct);
	CFRelease(svc); CFRelease(acct);
	if (keychain != NULL) {
		CFArrayRef list = CFArrayCreate(kCFAllocatorDefault, (const void **)&keychain, 1, &kCFTypeArrayCallBacks);
		CFDictionarySetValue(q, kSecMatchSearchList, list);
		CFRelease(list);
	}
	return q;
}

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
static OSStatus tp_open(const char *path, SecKeychainRef *out) {
	*out = NULL;
	if (path == NULL) return errSecSuccess;
	return SecKeychainOpen(path, out);
}
static void tp_no_ui(void) { SecKeychainSetUserInteractionAllowed(false); }

// TP_NOT_OWNED: the item exists but its decrypt access list is not "this
// process only" (any application, or another trusted application).
#define TP_NOT_OWNED 1

// tp_owned reports whether the item's decrypt ACLs trust only the calling
// process, so a same-user process cannot plant a key it knows (for example
// with `security add-generic-password -A`).
static OSStatus tp_owned(const char *path, const char *service, const char *account) {
	SecKeychainRef kc; OSStatus st = tp_open(path, &kc);
	if (st != errSecSuccess) return st;
	CFMutableDictionaryRef q = tp_query(service, account, kc);
	CFDictionarySetValue(q, kSecReturnRef, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
	CFTypeRef item = NULL;
	st = SecItemCopyMatching(q, &item);
	CFRelease(q);
	if (kc) CFRelease(kc);
	if (st != errSecSuccess) return st;
	SecAccessRef access = NULL;
	st = SecKeychainItemCopyAccess((SecKeychainItemRef)item, &access);
	CFRelease(item);
	if (st != errSecSuccess) return st;
	SecTrustedApplicationRef me = NULL;
	CFDataRef meData = NULL;
	st = SecTrustedApplicationCreateFromPath(NULL, &me);
	if (st == errSecSuccess) st = SecTrustedApplicationCopyData(me, &meData);
	if (me) CFRelease(me);
	if (st != errSecSuccess) { CFRelease(access); return st; }
	CFArrayRef acls = SecAccessCopyMatchingACLList(access, kSecACLAuthorizationDecrypt);
	CFRelease(access);
	OSStatus result = TP_NOT_OWNED;
	if (acls != NULL && CFArrayGetCount(acls) > 0) {
		result = errSecSuccess;
		for (CFIndex i = 0; i < CFArrayGetCount(acls) && result == errSecSuccess; i++) {
			CFArrayRef apps = NULL; CFStringRef desc = NULL;
			SecKeychainPromptSelector prompt;
			if (SecACLCopyContents((SecACLRef)CFArrayGetValueAtIndex(acls, i), &apps, &desc, &prompt) != errSecSuccess || apps == NULL) {
				result = TP_NOT_OWNED; // unreadable, or NULL = any application
			} else {
				for (CFIndex j = 0; j < CFArrayGetCount(apps); j++) {
					CFDataRef data = NULL;
					if (SecTrustedApplicationCopyData((SecTrustedApplicationRef)CFArrayGetValueAtIndex(apps, j), &data) != errSecSuccess
						|| !CFEqual(data, meData)) {
						result = TP_NOT_OWNED;
					}
					if (data) CFRelease(data);
					if (result != errSecSuccess) break;
				}
			}
			if (apps) CFRelease(apps);
			if (desc) CFRelease(desc);
		}
	}
	if (acls) CFRelease(acls);
	CFRelease(meData);
	return result;
}
#pragma clang diagnostic pop

// tp_read copies the item's data into *out (malloc'd), *outLen bytes.
static OSStatus tp_read(const char *path, const char *service, const char *account, void **out, size_t *outLen) {
	SecKeychainRef kc; OSStatus st = tp_open(path, &kc);
	if (st != errSecSuccess) return st;
	CFMutableDictionaryRef q = tp_query(service, account, kc);
	CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
	CFTypeRef result = NULL;
	st = SecItemCopyMatching(q, &result);
	CFRelease(q);
	if (kc) CFRelease(kc);
	if (st != errSecSuccess) return st;
	CFDataRef data = (CFDataRef)result;
	*outLen = (size_t)CFDataGetLength(data);
	*out = malloc(*outLen);
	memcpy(*out, CFDataGetBytePtr(data), *outLen);
	CFRelease(result);
	return errSecSuccess;
}

// tp_delete removes every matching item from the keychain at path.
static OSStatus tp_delete(const char *path, const char *service, const char *account) {
	SecKeychainRef kc; OSStatus st = tp_open(path, &kc);
	if (st != errSecSuccess) return st;
	CFMutableDictionaryRef q = tp_query(service, account, kc);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitAll);
	st = SecItemDelete(q);
	CFRelease(q);
	if (kc) CFRelease(kc);
	return st == errSecItemNotFound ? errSecSuccess : st;
}

// tp_add creates the item. The creating process (the daemon) is the only
// trusted application on its access list.
static OSStatus tp_add(const char *path, const char *service, const char *account, const void *key, size_t keyLen) {
	SecKeychainRef kc; OSStatus st = tp_open(path, &kc);
	if (st != errSecSuccess) return st;
	CFMutableDictionaryRef q = tp_query(service, account, NULL);
	if (kc) CFDictionarySetValue(q, kSecUseKeychain, kc);
	CFStringRef label = tp_str("Tallpine project environment key");
	CFDictionarySetValue(q, kSecAttrLabel, label);
	CFRelease(label);
	CFDataRef data = CFDataCreate(kCFAllocatorDefault, key, (CFIndex)keyLen);
	CFDictionarySetValue(q, kSecValueData, data);
	CFRelease(data);
	st = SecItemAdd(q, NULL);
	CFRelease(q);
	if (kc) CFRelease(kc);
	return st;
}
*/
import "C"

import (
	"context"
	"crypto/rand"
	"fmt"
	"os/user"
	"path/filepath"
	"sync"
	"testing"
	"unsafe"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Keychain is the macOS Security-framework key store.
type Keychain struct {
	opts Options
	mu   sync.Mutex
}

// New returns the platform key store.
func New(opts Options) ports.SecretKeyStore {
	if opts.Keychain == "" && testing.Testing() {
		return testGuard{} // tests never touch the user's login keychain
	}
	if opts.Keychain == "" {
		// Pinned to the login keychain file, never the whole search list, so a
		// keychain another process adds to the list cannot supply the item.
		path, err := loginKeychain()
		if err != nil {
			return unavailableStore{err: err}
		}
		opts.Keychain = path
	}
	if opts.NoUI {
		C.tp_no_ui()
	}
	return &Keychain{opts: opts}
}

// Key reads the key item, creating it with a fresh random key on first use.
func (k *Keychain) Key(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrSecretKeyUnavailable, err)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	key, st := k.read()
	if st == C.errSecItemNotFound {
		fresh := make([]byte, 32)
		if _, err := rand.Read(fresh); err != nil {
			return nil, fmt.Errorf("%w: %w", ports.ErrSecretKeyUnavailable, err)
		}
		if st = k.add(fresh); st == C.errSecSuccess || st == C.errSecDuplicateItem {
			key, st = k.read()
		}
	}
	if st != C.errSecSuccess {
		return nil, fmt.Errorf("%w: keychain status %d", ports.ErrSecretKeyUnavailable, int(st))
	}
	if st = k.owned(); st == C.TP_NOT_OWNED {
		return nil, fmt.Errorf("%w: keychain item %s is readable by other applications (not created by Tallpine); delete it and restart Tallpine", ports.ErrSecretKeyUnavailable, Service)
	} else if st != C.errSecSuccess {
		return nil, fmt.Errorf("%w: keychain access check status %d", ports.ErrSecretKeyUnavailable, int(st))
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: keychain item holds %d bytes", ports.ErrSecretKeyUnavailable, len(key))
	}
	return key, nil
}

func (k *Keychain) cstrings() (path, service, account *C.char, free func()) {
	service, account = C.CString(Service), C.CString(Account)
	if k.opts.Keychain != "" {
		path = C.CString(k.opts.Keychain)
	}
	return path, service, account, func() {
		C.free(unsafe.Pointer(service))
		C.free(unsafe.Pointer(account))
		if path != nil {
			C.free(unsafe.Pointer(path))
		}
	}
}

func (k *Keychain) read() ([]byte, C.OSStatus) {
	path, service, account, free := k.cstrings()
	defer free()
	var out unsafe.Pointer
	var n C.size_t
	st := C.tp_read(path, service, account, &out, &n)
	if st != C.errSecSuccess {
		return nil, st
	}
	defer C.free(out)
	return C.GoBytes(out, C.int(n)), st
}

// Recreate deletes any existing key item (which may have been planted by
// another process) and creates a fresh one, returning its key.
func (k *Keychain) Recreate(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrSecretKeyUnavailable, err)
	}
	k.mu.Lock()
	path, service, account, free := k.cstrings()
	st := C.tp_delete(path, service, account)
	free()
	k.mu.Unlock()
	if st != C.errSecSuccess {
		return nil, fmt.Errorf("%w: delete existing keychain item: status %d", ports.ErrSecretKeyUnavailable, int(st))
	}
	return k.Key(ctx)
}

func (k *Keychain) owned() C.OSStatus {
	path, service, account, free := k.cstrings()
	defer free()
	return C.tp_owned(path, service, account)
}

func (k *Keychain) add(key []byte) C.OSStatus {
	path, service, account, free := k.cstrings()
	defer free()
	ck := C.CBytes(key)
	defer C.free(ck)
	return C.tp_add(path, service, account, ck, C.size_t(len(key)))
}

type testGuard struct{}

func (testGuard) Key(context.Context) ([]byte, error) {
	return nil, fmt.Errorf("%w: login keychain is off limits in tests", ports.ErrSecretKeyUnavailable)
}

func loginKeychain() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("%w: no home directory for the login keychain: %w", ports.ErrSecretKeyUnavailable, err)
	}
	if u.HomeDir == "" {
		return "", fmt.Errorf("%w: no home directory for the login keychain", ports.ErrSecretKeyUnavailable)
	}
	return filepath.Join(u.HomeDir, "Library", "Keychains", "login.keychain-db"), nil
}

type unavailableStore struct{ err error }

func (u unavailableStore) Key(context.Context) ([]byte, error) { return nil, u.err }
