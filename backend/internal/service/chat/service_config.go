package chat

import (
	"context"
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ErrModelsUnsupported reports a driver whose provider cannot enumerate models.
// Distinct from an empty list: "this agent does not offer a choice" is a different
// answer for a client to render than "you have no models available".
var ErrModelsUnsupported = errors.New("chat driver cannot list models")

// ErrConfigOptionsUnsupported reports a conversation whose provider does not
// advertise live session controls. This is an ordinary capability answer: native
// drivers can continue using Tallpine's model/settings surface.
var ErrConfigOptionsUnsupported = errors.New("chat driver has no session config options")

// Models reports what the provider offers for this session, plus what is selected.
//
// Read from the live conversation rather than a table in Tallpine: models are added,
// renamed, hidden per account and gated by entitlement the provider knows about.
func (s *Service) Models(ctx context.Context, id domain.SessionID) ([]ports.ChatModel, domain.ConversationSettings, error) {
	controller, _, release, err := s.readingController(ctx, id)
	if err != nil {
		return nil, domain.ConversationSettings{}, err
	}
	defer release()
	return listModels(ctx, controller)
}

// ModelsForOwner reads the catalog from the owner-specific provider.
func (s *Service) ModelsForOwner(ctx context.Context, owner domain.ConversationOwner) ([]ports.ChatModel, domain.ConversationSettings, error) {
	controller, err := s.ControllerForOwner(owner)
	if err != nil {
		return nil, domain.ConversationSettings{}, err
	}
	return listModels(ctx, controller)
}

func listModels(ctx context.Context, controller *Controller) ([]ports.ChatModel, domain.ConversationSettings, error) {
	lister, ok := controller.conv.(ports.ChatModelLister)
	if !ok {
		return nil, controller.Settings(), ErrModelsUnsupported
	}
	models, err := lister.ListModels(ctx)
	if err != nil {
		return nil, controller.Settings(), err
	}
	return models, controller.Settings(), nil
}

// ConfigOptions reports the provider's live session controls. Unlike Tallpine's
// durable turn settings, these are provider-owned session state and are read from
// the connected conversation so model entitlements and model-dependent choices
// cannot go stale in a Tallpine table.
func (s *Service) ConfigOptions(ctx context.Context, id domain.SessionID) ([]ports.ChatConfigOption, error) {
	controller, record, release, err := s.readingController(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	configurer, ok := controller.conv.(ports.ChatConfigOptionController)
	if !ok {
		return nil, ErrConfigOptionsUnsupported
	}
	options, err := configurer.ListConfigOptions(ctx)
	return permissionConfigOptions(record.Harness, options), err
}

// SetConfigOption applies one provider-advertised value and returns the complete
// post-change catalog. Callers replace their list because changing the model can
// add or remove effort, fast-mode, and other dependent controls.
func (s *Service) SetConfigOption(
	ctx context.Context,
	id domain.SessionID,
	configID string,
	value ports.ChatConfigOptionValue,
) ([]ports.ChatConfigOption, error) {
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return nil, err
	}
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	configurer, ok := controller.conv.(ports.ChatConfigOptionController)
	if !ok {
		return nil, ErrConfigOptionsUnsupported
	}
	// A provider-owned model menu changes the model through this route, so the
	// project's model policy applies here as it does to the turn-settings route.
	if isModelConfigOption(ctx, configurer, configID) {
		if err := s.checkModelPolicy(ctx, id, value.Select); err != nil {
			return nil, err
		}
	}
	controller.configMu.Lock()
	defer controller.configMu.Unlock()
	previous := controller.Settings()
	options, err := configurer.SetConfigOption(ctx, configID, value)
	if err != nil {
		return nil, err
	}
	if len(options) == 0 {
		if listed, listErr := configurer.ListConfigOptions(ctx); listErr == nil {
			options = listed
		}
	}
	options = permissionConfigOptions(record.Harness, options)
	settings, _ := settingsFromConfigOptions(previous, options)
	if record.Harness == domain.HarnessClaudeCode {
		// Provider-owned defaults stay implicit so future Claude defaults still apply.
		if settings.Model == "default" {
			settings.Model = ""
		}
		if settings.ReasoningEffort == "default" {
			settings.ReasoningEffort = ""
		}
	}
	if settings != previous {
		if err := controller.SetSettings(ctx, settings); err != nil {
			return nil, err
		}
		// The config-options route is how provider-owned pickers (e.g. Claude
		// Code's model menu) change the model; it must persist the pick the same
		// way the turn-settings route does.
		s.persistPickedModel(id, previous, settings)
	}
	return options, nil
}

// isModelConfigOption reports whether configID is the provider's model option,
// matched the way settingsFromConfigOptions reads it (id or category "model").
// If the options cannot be listed, the conventional id decides.
func isModelConfigOption(ctx context.Context, configurer ports.ChatConfigOptionController, configID string) bool {
	if configID == "model" {
		return true
	}
	options, err := configurer.ListConfigOptions(ctx)
	if err != nil {
		return false
	}
	for _, option := range options {
		if option.ID == configID && option.Category == "model" {
			return true
		}
	}
	return false
}

func settingsFromConfigOptions(
	settings domain.ConversationSettings,
	options []ports.ChatConfigOption,
) (domain.ConversationSettings, bool) {
	next := settings
	hasEffort := false
	for _, option := range options {
		for _, choice := range option.Choices {
			if choice.Value == option.Current.Select && choice.PermissionMode != "" {
				next.ApprovalMode = choice.PermissionMode
			}
		}
		switch {
		case option.ID == "model" || option.Category == "model":
			if option.Current.Select != "" {
				next.Model = option.Current.Select
			}
		case option.ID == "effort" || option.Category == "thought_level":
			hasEffort = true
			next.ReasoningEffort = option.Current.Select
		}
	}
	if !hasEffort {
		next.ReasoningEffort = ""
	}
	return next, next != settings
}
