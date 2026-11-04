package codex

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func newCodexAccountManager(ctx context.Context, accountRoot, pendingRoot, switchStagingRoot, globalHome string, factory ports.CodexAccountClientFactory, logger *slog.Logger, operationGates ...ports.CodexOperationGate) *codexAccountManager {
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	var operationGate ports.CodexOperationGate
	if len(operationGates) > 0 {
		operationGate = operationGates[0]
	}
	m := &codexAccountManager{
		ctx: ctx, catalog: newCodexAccountCatalog(accountRoot, logger), factory: factory, operationGate: operationGate,
		logger: logger, now: func() time.Time { return time.Now().UTC() }, newID: uuid.NewString,
		after:     time.After,
		processes: make(chan struct{}, codexAccountProcessLimit), mutations: make(chan struct{}, 1), executable: os.Executable,
		globalHome: canonicalPath(globalHome), pendingRoot: canonicalPath(pendingRoot), switchStagingRoot: canonicalPath(switchStagingRoot),
		auth: map[string]*accountAuthState{}, usage: map[string]*accountUsageState{},
		capabilities: unavailableCodexCapabilities(), subscribers: map[chan Accounts]struct{}{},
		snapshotRevision: time.Now().UTC().UnixNano(),
		reconciliation: domain.CodexDeviceReconciliation{
			Status: domain.CodexDeviceReconciliationNotChecked, ReasonCode: "not_checked",
		},
	}
	m.mutations <- struct{}{}
	m.capacity = newCodexCapacityCoordinator(m)
	m.catalog.setOnRemoved(func(ids []string) { m.capacity.removeAccounts(ids); m.publish() })
	return m
}

func (m *codexAccountManager) acquireAccountMutation(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-m.mutations:
		var once sync.Once
		return func() { once.Do(func() { m.mutations <- struct{}{} }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *codexAccountManager) acquireGlobalRead(ctx context.Context, account ports.CodexAccountContext) (func(), error) {
	if m.operationGate == nil || canonicalPath(account.Home) != m.globalHome {
		return func() {}, nil
	}
	return m.operationGate.AcquireShared(ctx)
}

func (m *codexAccountManager) acquireGlobalMutation(ctx context.Context) (ports.CodexOperationLease, error) {
	if m.operationGate == nil {
		return nil, nil
	}
	return m.operationGate.AcquireExclusive(ctx)
}

func unavailableCodexCapabilities() domain.CodexAccountCapabilities {
	unknown := domain.CodexCapabilityObservation{State: domain.CodexCapabilityUnknown, ReasonCode: domain.CodexCapabilityReasonUnknown, Reason: "Codex capability detection has not completed."}
	return domain.CodexAccountCapabilities{AccountRead: unknown, NativeLogin: unknown, CapacityRead: unknown, UsageRead: unknown, ResetCreditConsume: unknown, GlobalSwitch: unknown}
}

func (m *codexAccountManager) detectCapabilities(ctx context.Context) domain.CodexAccountCapabilities {
	if m.factory == nil {
		return unavailableCodexCapabilities()
	}
	capabilities := m.factory.Capabilities(ctx)
	if err := m.validateGlobalCredentialStore(); err != nil {
		capabilities.GlobalSwitch = domain.CodexCapabilityObservation{
			State: domain.CodexCapabilityUnsupported, ReasonCode: "global_credential_store_unsupported",
			Reason: "Device-global account switching requires a file-backed Codex sign-in.",
		}
	} else {
		capabilities.GlobalSwitch = domain.CodexCapabilityObservation{
			State: domain.CodexCapabilitySupported, ReasonCode: domain.CodexCapabilityReasonSupported,
			Reason: "Tallpine can switch file-backed Codex credentials on this device.",
		}
	}
	m.mu.Lock()
	m.capabilities = capabilities
	m.mu.Unlock()
	return capabilities
}

func (m *codexAccountManager) view(ids []string) (Accounts, error) {
	records, err := m.catalog.recordsFor(ids)
	if err != nil {
		return Accounts{}, mapUnknownCodexAccount(err)
	}
	m.mu.Lock()
	snapshotRevision, capabilities, reconciliation := m.snapshotRevision, m.capabilities, m.reconciliation
	deviceAccountID := ""
	if reconciliation.ActiveAccountVerified {
		deviceAccountID = m.deviceAccountID
	}
	var activeLogin *ActiveLogin
	if m.login != nil && m.login.terminalHandle != "" && !terminalLoginStatus(m.login.snapshot.Status) {
		activeLogin = &ActiveLogin{
			OperationID: m.login.snapshot.OperationID, AccountID: m.login.snapshot.AccountID,
			Status: m.login.snapshot.Status, ReasonCode: m.login.snapshot.ReasonCode, Reason: m.login.snapshot.Reason,
			ExpiresAt: m.login.snapshot.ExpiresAt,
			ShellTerminal: LoginTerminalDisplay{
				HandleID: m.login.terminalHandle, Title: m.login.terminalTitle, CreatedAt: m.login.terminalCreated,
			},
		}
	}
	m.mu.Unlock()
	accounts := make([]domain.CodexAccountSnapshot, 0, len(records))
	for _, record := range records {
		snapshot := record.Snapshot
		snapshot.Active = deviceAccountID != "" && snapshot.ID == deviceAccountID
		snapshot.Capacity = m.capacity.snapshot(snapshot.ID)
		m.mu.Lock()
		if usage := m.usage[snapshot.ID]; usage != nil && usage.value != nil {
			usageCopy := *usage.value
			snapshot.UsageSummary = &usageCopy
		}
		m.mu.Unlock()
		accounts = append(accounts, snapshot)
	}
	if deviceAccountID != "" {
		for i := range accounts {
			if accounts[i].ID == deviceAccountID && i > 0 {
				item := accounts[i]
				copy(accounts[1:i+1], accounts[0:i])
				accounts[0] = item
				break
			}
		}
	}
	return Accounts{ActiveAccountID: deviceAccountID, AccountRevision: snapshotRevision, Accounts: accounts, Capabilities: capabilities, DeviceReconciliation: reconciliation, ActiveLogin: activeLogin}, nil
}

func (m *codexAccountManager) cached() Accounts { result, _ := m.view(nil); return result }

func (m *codexAccountManager) accountContext(record codexAccountRecord) ports.CodexAccountContext {
	if record.useSavedHome {
		return ports.CodexAccountContext{Home: record.Home, Managed: true}
	}
	home := record.Home
	m.mu.Lock()
	active, associated := m.deviceAccountID, m.reconciliation.ActiveAccountVerified
	m.mu.Unlock()
	// The global home is safe for a saved slot only after the current device
	// credential has been positively associated with that exact slot.
	if associated && record.Snapshot.ID == active {
		return ports.CodexAccountContext{Home: m.globalHome, Managed: false}
	}
	return ports.CodexAccountContext{Home: home, Managed: true}
}

func (m *codexAccountManager) deferAccountRead(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	associated := m.reconciliation.ActiveAccountVerified && id == m.deviceAccountID
	return id != "" && id == m.deferredAccountID && !associated
}
