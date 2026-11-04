package codex

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// PrepareCodexAccountForSwitch snapshots the stable source and target
// credentials before the durable journal is created. Provider state is not
// consulted; the staged files are the complete local recovery evidence.
func (s *Service) PrepareCodexAccountForSwitch(ctx context.Context, switchID, accountID string) (domain.CodexAccountSwitchSource, error) {
	notPrepared := func(err error) (domain.CodexAccountSwitchSource, error) {
		return domain.CodexAccountSwitchSource{}, errors.Join(ports.ErrCodexAccountSwitchNotCommitted, err)
	}
	if s.codexAccounts == nil {
		return notPrepared(apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable"))
	}
	if s.CodexAccountLoginInProgress() {
		return notPrepared(apierr.Conflict("CODEX_ACCOUNT_LOGIN_IN_PROGRESS", "Finish or close the Codex account login before switching accounts", nil))
	}
	if !isCanonicalUUIDv4(strings.TrimSpace(switchID)) {
		return notPrepared(apierr.Invalid("INVALID_CODEX_ACCOUNT_ID", "Invalid Codex account switch identifier", nil))
	}
	record, ok := s.codexAccounts.catalog.record(strings.TrimSpace(accountID))
	if !ok || record.Snapshot.Status != domain.CodexAccountStatusValid {
		return notPrepared(apierr.NotFound("CODEX_ACCOUNT_NOT_FOUND", "Codex account not found"))
	}
	if err := ctx.Err(); err != nil {
		return notPrepared(err)
	}
	credentialPath := filepath.Join(record.Home, codexCredentialFilename)
	credential, admitted, credentialErr := readCodexFileState(credentialPath, false)
	if credentialErr != nil {
		s.codexAccounts.requireReauthentication(record.Snapshot.ID)
		return notPrepared(apierr.Conflict("CODEX_ACCOUNT_REAUTHENTICATION_REQUIRED", "Sign in again before switching to this Codex account", nil))
	}
	latestCredential, latest, latestErr := readCodexFileState(credentialPath, false)
	if latestErr != nil || !sameCodexFileState(admitted, latest) || !bytes.Equal(credential, latestCredential) {
		return notPrepared(apierr.Conflict("CODEX_ACCOUNT_IDENTITY_CHANGED", "The Codex account changed while preparing the switch. Try again", nil))
	}
	if !localCredentialIdentifiesRecord(record, latestCredential) {
		return notPrepared(apierr.Conflict("CODEX_ACCOUNT_IDENTITY_CHANGED", "The saved Codex account no longer matches its credential. Sign in again", nil))
	}
	_ = s.codexAccounts.catalog.updateCredentialIdentity(ctx, record.Snapshot.ID, latestCredential)

	stagingDir := filepath.Join(s.codexAccounts.switchStagingRoot, switchID)
	if err := ensurePrivateDirectory(stagingDir); err != nil {
		return notPrepared(apierr.Unavailable("CODEX_ACCOUNT_SWITCH_ACTIVATION_UNCONFIRMED", "The Codex credential switch could not be staged"))
	}
	if err := writePrivateFileAtomic(filepath.Join(stagingDir, "target-auth.json"), latestCredential); err != nil {
		_ = os.RemoveAll(stagingDir)
		return notPrepared(apierr.Unavailable("CODEX_ACCOUNT_SWITCH_ACTIVATION_UNCONFIRMED", "The selected Codex credential could not be staged"))
	}

	globalCredential, globalState, globalErr := readCodexDeviceFileState(s.codexAccounts.globalCredentialPath(), true)
	if globalErr != nil {
		_ = os.RemoveAll(stagingDir)
		return notPrepared(apierr.NotImplemented("CODEX_GLOBAL_CREDENTIAL_STORE_UNSUPPORTED", "Device-global Codex account switching requires file-backed credentials"))
	}
	source := domain.CodexAccountSwitchSource{Kind: domain.CodexAccountSwitchSourceNone}
	if globalState.exists {
		identity, identityErr := parseCodexCredentialIdentity(globalCredential)
		matched, match := s.codexAccounts.matchGlobalCredentialForReconciliation(globalCredential, identity, identityErr)
		source.Kind = domain.CodexAccountSwitchSourceDevice
		if match == codexCredentialMatchManaged {
			source.Kind = domain.CodexAccountSwitchSourceManaged
			source.AccountID = matched.Snapshot.ID
		}
		if err := writePrivateFileAtomic(filepath.Join(stagingDir, "source-auth.json"), globalCredential); err != nil {
			_ = os.RemoveAll(stagingDir)
			return notPrepared(apierr.Unavailable("CODEX_ACCOUNT_SWITCH_ACTIVATION_UNCONFIRMED", "The current Codex credential could not be checkpointed"))
		}
	}
	finalGlobal, finalState, finalErr := readCodexDeviceFileState(s.codexAccounts.globalCredentialPath(), true)
	if finalErr != nil || !sameCodexFileState(globalState, finalState) || !bytes.Equal(globalCredential, finalGlobal) {
		_ = os.RemoveAll(stagingDir)
		return notPrepared(ports.ErrCodexGlobalAccountChanged)
	}
	return source, nil
}

// InspectCodexAccountSwitch classifies the stable device credential against
// the staged target and source. An error means local inspection was
// inconclusive and the durable switch fence must be retained.
func (s *Service) InspectCodexAccountSwitch(ctx context.Context, switchID string, sourceKind domain.CodexAccountSwitchSourceKind, sourceAccountID, accountID string) (domain.CodexAccountSwitchInstallationState, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.codexAccounts == nil {
		return "", apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable")
	}
	switchID = strings.TrimSpace(switchID)
	if !isCanonicalUUIDv4(switchID) {
		return "", apierr.Invalid("INVALID_CODEX_ACCOUNT_ID", "Invalid Codex account switch identifier", nil)
	}
	accountID = strings.TrimSpace(accountID)
	if err := s.codexAccounts.validateGlobalCredentialStore(); err != nil {
		return "", apierr.NotImplemented("CODEX_GLOBAL_CREDENTIAL_STORE_UNSUPPORTED", "Device-global Codex account switching requires file-backed credentials")
	}
	globalPath := s.codexAccounts.globalCredentialPath()
	globalCredential, admitted, credentialErr := readCodexDeviceFileState(globalPath, true)
	if credentialErr != nil {
		return "", credentialErr
	}
	latestCredential, latest, latestErr := readCodexDeviceFileState(globalPath, true)
	if latestErr != nil || !sameCodexFileState(admitted, latest) || !bytes.Equal(globalCredential, latestCredential) {
		return "", errors.Join(ports.ErrCodexGlobalAccountChanged, latestErr)
	}
	if !admitted.exists {
		return domain.CodexAccountSwitchCredentialMissing, nil
	}
	targetCredential, targetErr := readOpaqueCredential(filepath.Join(s.codexAccounts.switchStagingRoot, switchID, "target-auth.json"))
	targetFromStaging := targetErr == nil
	var targetRecord codexAccountRecord
	if targetErr != nil && !errors.Is(targetErr, os.ErrNotExist) {
		return "", targetErr
	}
	if !targetFromStaging {
		if record, ok := s.codexAccounts.catalog.record(accountID); ok && record.Snapshot.Status == domain.CodexAccountStatusValid {
			targetRecord = record
			targetCredential, targetErr = readOpaqueCredential(filepath.Join(record.Home, codexCredentialFilename))
			if targetErr != nil && !errors.Is(targetErr, os.ErrNotExist) {
				return "", targetErr
			}
		}
	}
	if targetErr == nil && bytes.Equal(globalCredential, targetCredential) && (targetFromStaging || localCredentialIdentifiesRecord(targetRecord, globalCredential)) {
		return domain.CodexAccountSwitchTargetInstalled, nil
	}
	if sourceKind != domain.CodexAccountSwitchSourceNone {
		sourceCredential, sourceErr := readOpaqueCredential(filepath.Join(s.codexAccounts.switchStagingRoot, switchID, "source-auth.json"))
		if sourceErr == nil && bytes.Equal(globalCredential, sourceCredential) {
			return domain.CodexAccountSwitchSourceInstalled, nil
		}
		if sourceErr != nil && !errors.Is(sourceErr, os.ErrNotExist) {
			return "", sourceErr
		}
		if sourceAccountID = strings.TrimSpace(sourceAccountID); sourceAccountID != "" {
			if source, found := s.codexAccounts.catalog.record(sourceAccountID); found && localCredentialIdentifiesRecord(source, globalCredential) {
				return domain.CodexAccountSwitchSourceInstalled, nil
			}
		}
	}
	return domain.CodexAccountSwitchExternalCredential, nil
}

// ActivatePreparedCodexAccountSwitch performs the single compare-and-swap
// from the staged source to the staged target.
func (s *Service) ActivatePreparedCodexAccountSwitch(ctx context.Context, sourceKind domain.CodexAccountSwitchSourceKind, switchID, targetID string) error {
	notCommitted := func(err error) error {
		return errors.Join(ports.ErrCodexAccountSwitchNotCommitted, err)
	}
	if s.codexAccounts == nil {
		return notCommitted(apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable"))
	}
	if !isCanonicalUUIDv4(strings.TrimSpace(switchID)) {
		return notCommitted(apierr.Invalid("INVALID_CODEX_ACCOUNT_ID", "Invalid Codex account switch identifier", nil))
	}
	stagingDir := filepath.Join(s.codexAccounts.switchStagingRoot, switchID)
	if sourceKind != domain.CodexAccountSwitchSourceManaged && sourceKind != domain.CodexAccountSwitchSourceDevice && sourceKind != domain.CodexAccountSwitchSourceNone {
		return notCommitted(apierr.Invalid("INVALID_CODEX_ACCOUNT_SWITCH_SOURCE", "Invalid Codex account switch source", nil))
	}
	target, ok := s.codexAccounts.catalog.record(strings.TrimSpace(targetID))
	if !ok || target.Snapshot.Status != domain.CodexAccountStatusValid {
		return notCommitted(apierr.NotFound("CODEX_ACCOUNT_NOT_FOUND", "Codex account not found"))
	}
	targetCredential, targetState, targetErr := readCodexFileState(filepath.Join(stagingDir, "target-auth.json"), false)
	latestTarget, latestTargetState, latestTargetErr := readCodexFileState(filepath.Join(stagingDir, "target-auth.json"), false)
	if targetErr != nil || latestTargetErr != nil || !sameCodexFileState(targetState, latestTargetState) || !bytes.Equal(targetCredential, latestTarget) || !localCredentialIdentifiesRecord(target, latestTarget) {
		s.codexAccounts.requireReauthentication(target.Snapshot.ID)
		return notCommitted(apierr.Conflict("CODEX_ACCOUNT_REAUTHENTICATION_REQUIRED", "Sign in again before switching to this Codex account", nil))
	}
	var expectedGlobal []byte
	if sourceKind == domain.CodexAccountSwitchSourceNone {
		expectedGlobal = []byte{}
	} else {
		var sourceErr error
		expectedGlobal, sourceErr = readOpaqueCredential(filepath.Join(stagingDir, "source-auth.json"))
		if sourceErr != nil {
			return notCommitted(apierr.Conflict("CODEX_GLOBAL_ACCOUNT_CHANGED", "The device Codex account changed before switching", nil))
		}
	}
	if err := ctx.Err(); err != nil {
		return notCommitted(err)
	}
	err := s.codexAccounts.activateFromCredentialLocked(ctx, strings.TrimSpace(targetID), filepath.Join(stagingDir, "target-auth.json"), expectedGlobal)
	if err == nil {
		s.invalidateAuthentication()
	}
	return err
}

// CleanupCodexAccountSwitch removes private switch staging after the durable
// switch has reached a locally confirmed terminal phase.
func (s *Service) CleanupCodexAccountSwitch(_ context.Context, switchID string) error {
	if s.codexAccounts == nil || !isCanonicalUUIDv4(strings.TrimSpace(switchID)) {
		return nil
	}
	return os.RemoveAll(filepath.Join(s.codexAccounts.switchStagingRoot, switchID))
}

// CleanupInactiveCodexAccountSwitches removes credential staging that has no
// live durable journal. The active switch directory, when supplied, is kept.
func (s *Service) CleanupInactiveCodexAccountSwitches(ctx context.Context, activeSwitchID string) error {
	if s.codexAccounts == nil {
		return nil
	}
	entries, err := os.ReadDir(s.codexAccounts.switchStagingRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	activeSwitchID = strings.TrimSpace(activeSwitchID)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if !entry.IsDir() || !isCanonicalUUIDv4(name) || name == activeSwitchID {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.codexAccounts.switchStagingRoot, name)); err != nil {
			return err
		}
	}
	return nil
}

var _ ports.CodexAccountCredentialManager = (*Service)(nil)

// CodexAccountSwitchInProgress reports whether the credential coordinator owns
// the device-global mutation gate.
func (s *Service) CodexAccountSwitchInProgress() bool {
	return s.codexSwitches != nil && s.codexSwitches.CodexAccountSwitchInProgress()
}

// StartCodexAccountSwitch starts a durable device credential switch.
func (s *Service) StartCodexAccountSwitch(ctx context.Context, cfg ports.CodexAccountSwitchConfig) (domain.CodexAccountSwitch, error) {
	if s.codexSwitches == nil {
		return domain.CodexAccountSwitch{}, apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account switching is unavailable")
	}
	return s.codexSwitches.StartCodexAccountSwitch(ctx, cfg)
}

// GetCodexAccountSwitch returns the durable result for one switch. The UI uses
// this journal state instead of inferring success from a reconciliation snapshot.
func (s *Service) GetCodexAccountSwitch(ctx context.Context, id string) (domain.CodexAccountSwitch, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return domain.CodexAccountSwitch{}, apierr.Invalid("CODEX_ACCOUNT_SWITCH_ID_REQUIRED", "Codex account switch ID is required", nil)
	}
	if s.codexSwitches == nil {
		return domain.CodexAccountSwitch{}, apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account switching is unavailable")
	}
	sw, ok, err := s.codexSwitches.GetCodexAccountSwitch(ctx, id)
	if err != nil {
		return domain.CodexAccountSwitch{}, err
	}
	if !ok {
		return domain.CodexAccountSwitch{}, apierr.NotFound("CODEX_ACCOUNT_SWITCH_NOT_FOUND", "Codex account switch not found")
	}
	return sw, nil
}

// ReconcileCodexAccountSwitches starts best-effort local settlement for any
// durable switch journal. Recovery failure never blocks unrelated daemon work.
func (s *Service) ReconcileCodexAccountSwitches(ctx context.Context) error {
	if s.codexSwitches == nil {
		return nil
	}
	return s.codexSwitches.ReconcileCodexAccountSwitches(ctx)
}

// WaitCodexAccountSwitchWorkers drains credential workers during shutdown.
func (s *Service) WaitCodexAccountSwitchWorkers(ctx context.Context) error {
	if s.codexSwitches == nil {
		return nil
	}
	return s.codexSwitches.Wait(ctx)
}
