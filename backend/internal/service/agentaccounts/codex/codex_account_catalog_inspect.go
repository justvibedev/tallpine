package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func (c *codexAccountCatalog) readManaged(id string) codexAccountRecord {
	descriptor, err := c.descriptors.get(context.Background(), id)
	return c.inspectManaged(id, descriptor, err)
}

// inspectManaged validates a stored descriptor against the account's folders
// and builds its record.
func (c *codexAccountCatalog) inspectManaged(id string, descriptor codexAccountDescriptor, err error) codexAccountRecord {
	accountDir := filepath.Join(c.root, id)
	home := filepath.Join(accountDir, codexCredentialHomeDirectory)
	broken := func(code, reason string) codexAccountRecord {
		return codexAccountRecord{Home: home, Snapshot: domain.CodexAccountSnapshot{
			ID: id, Label: "Unavailable Codex account", Source: domain.CodexAccountSourceManaged,
			Status: domain.CodexAccountStatusBroken, ReasonCode: code, Reason: reason,
			Authentication: uncheckedAuthentication(), AuthMethod: domain.CodexAuthMethodUnknown,
			Capacity: unavailableCodexCapacity(),
		}}
	}
	if dirErr := validateCodexDirectory(accountDir, true); dirErr != nil {
		return broken(domain.CodexAccountReasonUnsafePath, "This Codex account has an unsafe directory layout.")
	}
	if errors.Is(err, errAccountStoreRead) {
		return broken(domain.CodexAccountReasonDescriptorInvalid, "The saved Codex accounts could not be read.")
	}
	if err != nil || descriptor.ID != id || descriptor.Source != domain.CodexAccountSourceManaged || !validAccountAuthMethod(descriptor.AuthMethod) || (descriptor.AccountEmail != nil && !safeAccountEmail(*descriptor.AccountEmail)) || (descriptor.ProviderAccountID != "" && !safeProviderAccountID(descriptor.ProviderAccountID)) || descriptor.CreatedAt.IsZero() || descriptor.VerifiedAt.IsZero() {
		return broken(domain.CodexAccountReasonDescriptorInvalid, "This Codex account descriptor is invalid.")
	}
	_, err = os.Lstat(home)
	if errors.Is(err, os.ErrNotExist) {
		return broken(domain.CodexAccountReasonHomeMissing, "This Codex account credential home is missing.")
	}
	if err != nil || validateCodexDirectory(home, true) != nil || !pathWithin(c.root, home) {
		return broken(domain.CodexAccountReasonUnsafePath, "This Codex account has an unsafe credential home.")
	}
	view := domain.CodexAccountViewOf(accountOf(descriptor, home), accountLabel(descriptor.AccountEmail))
	credentialPath := filepath.Join(home, codexCredentialFilename)
	credentialState, err := inspectCodexFile(credentialPath, true)
	if errors.Is(err, os.ErrNotExist) {
		return broken(domain.CodexAccountReasonUnsafePath, "This Codex account credential is unavailable or unsafe.")
	}
	if err == nil && !credentialState.exists {
		return codexAccountRecord{Home: canonicalPath(home), ProviderAccountID: descriptor.ProviderAccountID, CreatedAt: descriptor.CreatedAt, VerifiedAt: descriptor.VerifiedAt, Snapshot: withObserved(view, domain.CodexAccountStatusSignedOut, domain.CodexAccountReasonSignedOut, "This Codex account is signed out.",
			signedOutAuthentication(c.now(), "Sign in again to use this Codex account."), unavailableCodexCapacity())}
	}
	if err != nil {
		return broken(domain.CodexAccountReasonUnsafePath, "This Codex account credential is unavailable or unsafe.")
	}
	return codexAccountRecord{Home: canonicalPath(home), ProviderAccountID: descriptor.ProviderAccountID, CreatedAt: descriptor.CreatedAt, VerifiedAt: descriptor.VerifiedAt, Snapshot: withObserved(view, domain.CodexAccountStatusValid, domain.CodexAccountReasonValid, "This Codex account is available.",
		uncheckedAuthentication(), uncheckedCodexCapacity())}
}

// withObserved adds the read-time state to a view derived from the saved row.
func withObserved(view domain.CodexAccountSnapshot, status domain.CodexAccountStatus, code, reason string, auth domain.AgentAuthenticationObservation, capacity domain.CodexCapacitySnapshot) domain.CodexAccountSnapshot {
	view.Status, view.ReasonCode, view.Reason, view.Authentication, view.Capacity = status, code, reason, auth, capacity
	return view
}
