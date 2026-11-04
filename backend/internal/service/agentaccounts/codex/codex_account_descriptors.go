package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// descriptorStore persists the descriptor of each Tallpine-managed Codex
// account. domain.AgentAccount rows in the account store are the only
// persisted model; credentials stay in the account's credential home.
type descriptorStore interface {
	list(ctx context.Context) ([]codexAccountDescriptor, error)
	// get returns os.ErrNotExist when no such managed account is saved.
	get(ctx context.Context, id string) (codexAccountDescriptor, error)
	create(ctx context.Context, descriptor codexAccountDescriptor, home string) error
	update(ctx context.Context, descriptor codexAccountDescriptor) error
	remove(ctx context.Context, id string) error
}

// accountRowDescriptors keeps descriptors in agent_accounts rows.
type accountRowDescriptors struct {
	store ports.CodexAccountRegistryStore
}

func descriptorOf(a domain.AgentAccount) codexAccountDescriptor {
	d := codexAccountDescriptor{
		ID: a.ID, Source: domain.CodexAccountSource(a.Source), AuthMethod: domain.CodexAuthMethod(a.AuthMethod),
		ProviderAccountID: a.ProviderAccountID, CreatedAt: a.CreatedAt,
	}
	if a.Email != "" {
		email := a.Email
		d.AccountEmail = &email
	}
	if a.VerifiedAt != nil {
		d.VerifiedAt = *a.VerifiedAt
	}
	return d
}

// errAccountStoreRead marks a failure to read the account store itself, as
// opposed to a missing or invalid account.
var errAccountStoreRead = errors.New("codex account store could not be read")

func (r accountRowDescriptors) managed(ctx context.Context) ([]domain.AgentAccount, error) {
	accounts, err := r.store.ListAgentAccounts(ctx)
	if err != nil {
		return nil, errors.Join(errAccountStoreRead, err)
	}
	out := accounts[:0:0]
	for _, a := range accounts {
		if a.Provider == domain.AgentAccountProviderCodex && a.Managed() {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r accountRowDescriptors) list(ctx context.Context) ([]codexAccountDescriptor, error) {
	accounts, err := r.managed(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]codexAccountDescriptor, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, descriptorOf(a))
	}
	return out, nil
}

func (r accountRowDescriptors) get(ctx context.Context, id string) (codexAccountDescriptor, error) {
	accounts, err := r.managed(ctx)
	if err != nil {
		return codexAccountDescriptor{}, err
	}
	for _, a := range accounts {
		if a.ID == id {
			return descriptorOf(a), nil
		}
	}
	return codexAccountDescriptor{}, os.ErrNotExist
}

// accountOf is the one mapping from a descriptor to its AgentAccount row; the
// catalog and the registry import both save accounts through it.
func accountOf(d codexAccountDescriptor, home string) domain.AgentAccount {
	account := domain.AgentAccount{
		ID: d.ID, Provider: domain.AgentAccountProviderCodex, Home: home, CreatedAt: d.CreatedAt,
		Source: string(d.Source), AuthMethod: string(d.AuthMethod), ProviderAccountID: d.ProviderAccountID,
	}
	if d.AccountEmail != nil {
		account.Email = *d.AccountEmail
	}
	if !d.VerifiedAt.IsZero() {
		verified := d.VerifiedAt
		account.VerifiedAt = &verified
	}
	return account
}

func (r accountRowDescriptors) create(ctx context.Context, d codexAccountDescriptor, home string) error {
	return r.store.CreateAgentAccount(ctx, accountOf(d, home))
}

func (r accountRowDescriptors) update(ctx context.Context, d codexAccountDescriptor) error {
	email := ""
	if d.AccountEmail != nil {
		email = *d.AccountEmail
	}
	return r.store.UpdateAgentAccountCodexDescriptor(ctx, d.ID, email, string(d.AuthMethod), d.ProviderAccountID, d.VerifiedAt)
}

func (r accountRowDescriptors) remove(ctx context.Context, id string) error {
	return r.store.DeleteAgentAccount(ctx, id)
}

// memoryDescriptors is the descriptor store when no account store is
// configured (account management unavailable, tests): nothing survives a
// restart.
type memoryDescriptors struct {
	mu    sync.Mutex
	items map[string]codexAccountDescriptor
}

func (m *memoryDescriptors) list(context.Context) ([]codexAccountDescriptor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]codexAccountDescriptor, 0, len(m.items))
	for _, d := range m.items {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *memoryDescriptors) get(_ context.Context, id string) (codexAccountDescriptor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.items[id]
	if !ok {
		return codexAccountDescriptor{}, os.ErrNotExist
	}
	return d, nil
}

func (m *memoryDescriptors) create(_ context.Context, d codexAccountDescriptor, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil {
		m.items = map[string]codexAccountDescriptor{}
	}
	m.items[d.ID] = d
	return nil
}

func (m *memoryDescriptors) update(_ context.Context, d codexAccountDescriptor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[d.ID]; !ok {
		return ports.ErrAgentAccountUnknown
	}
	m.items[d.ID] = d
	return nil
}

func (m *memoryDescriptors) remove(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, id)
	return nil
}

// accountHome is where a managed account's credential lives.
func accountHome(root, id string) string {
	return filepath.Join(root, id, codexCredentialHomeDirectory)
}
