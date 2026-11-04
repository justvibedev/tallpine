package acp

import (
	"context"
	"errors"
	"fmt"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func (c *conversation) applyTurnSettings(ctx context.Context, settings ports.ChatTurnSettings) error {
	c.mu.Lock()
	sessionID := c.sessionID
	modeFor := c.modeFor
	optionsFor := c.optionsFor
	initialPermission := c.initialPermission
	validateSettings := c.validateSettings
	legacyModel := c.legacyModel
	legacyMode := c.legacyMode
	configOptions := cloneConfigOptions(c.configOptions)
	c.mu.Unlock()
	if sessionID == "" {
		return errors.New("ACP session is not open")
	}
	if validateSettings != nil {
		if err := validateSettings(initialPermission, settings); err != nil {
			return err
		}
	}
	var options []SessionOption
	if optionsFor != nil {
		options = optionsFor(settings)
	}
	// A setting the session already has is skipped (some agents take seconds
	// per change) unless session/set_model made the list stale.
	catalogStale := false
	if legacyModel && settings.Model != "" {
		// A binding may name the model differently from the turn settings.
		model := settings.Model
		for _, option := range options {
			if option.ID == "model" && option.Value != "" {
				model = option.Value
			}
		}
		modelOptionFound := false
		for _, option := range configOptions {
			if option.ID != "model" {
				continue
			}
			modelOptionFound = true
			resolved, ok := resolveLegacyModelChoice(option.Choices, model)
			if !ok {
				return fmt.Errorf("%w: ACP session model does not offer %q", ports.ErrChatConfigOptionInvalid, model)
			}
			model = resolved
			break
		}
		if !modelOptionFound {
			return fmt.Errorf("%w: ACP session does not advertise a model option", ports.ErrChatConfigOptionInvalid)
		}
		if c.configOptionCurrent("model") != model {
			if err := c.legacyWire.setModel(ctx, sessionID, model); err != nil {
				if isACPMethodNotFound(err) {
					return fmt.Errorf("%w: session/set_model %q", ErrACPSetterUnsupported, model)
				}
				return fmt.Errorf("set ACP session model %q: %w", model, err)
			}
			catalogStale = true
		}
		c.applyAcceptedConfigOption("model", ports.ChatConfigOptionValue{Select: model})
	}
	applyOption := func(option SessionOption) error {
		if option.ID == "" || option.Value == "" {
			return nil
		}
		if (option.ID == "model" && legacyModel) || (option.ID == "mode" && legacyMode) {
			return nil
		}
		if offered, known := c.configOptionOffers(option.ID, option.Value); known && !offered && option.Name != "" {
			if value := c.configOptionValueNamed(option.ID, option.Name); value != "" {
				option.Value = value
			}
		}
		if !catalogStale && c.configOptionCurrent(option.ID) == option.Value {
			return nil
		}
		// The models offered do not depend on the model chosen, so this is final.
		if offered, known := c.configOptionOffers(option.ID, option.Value); option.Optional && option.ID == "model" && known && !offered {
			return nil
		}
		return c.setSessionOption(ctx, sessionID, option)
	}
	// A model switch may change the modes an ACP agent offers. Apply a modern
	// model config option before session/set_mode so the requested mode is
	// validated against the selected model rather than the session's default.
	for _, option := range options {
		if option.ID == "model" {
			if err := applyOption(option); err != nil {
				return err
			}
		}
	}
	effectiveApproval := settings.Approval
	if modeFor != nil {
		if mode := modeFor(settings.Approval); mode != "" {
			// Some ACP agents rebuild their permission modes after a model switch.
			// Claude, for example, removes Auto for Haiku and clamps itself back to
			// Default. Respect that authoritative catalog instead of immediately
			// reapplying Tallpine's default Auto choice and aborting session creation.
			if ports.NormalizePermissionMode(settings.Approval) == ports.PermissionModeAuto {
				if offered, known := c.configOptionOffers("mode", mode); known && !offered {
					if fallbackOffered, _ := c.configOptionOffers("mode", "default"); fallbackOffered {
						mode = "default"
						effectiveApproval = ports.PermissionModeDefault
					}
				}
			}
			if _, err := c.conn.SetSessionMode(ctx, acpsdk.SetSessionModeRequest{
				SessionId: acpsdk.SessionId(sessionID), ModeId: acpsdk.SessionModeId(mode),
			}); err != nil {
				if isACPMethodNotFound(err) {
					return fmt.Errorf("%w: session/set_mode %q", ErrACPSetterUnsupported, mode)
				}
				return fmt.Errorf("set ACP session mode %q: %w", mode, err)
			}
		}
	}
	for _, option := range options {
		if option.ID != "model" {
			if err := applyOption(option); err != nil {
				return err
			}
		}
	}
	if settings.Approval != "" {
		c.mu.Lock()
		c.permissionMode = ports.NormalizePermissionMode(effectiveApproval)
		c.mu.Unlock()
	}
	return nil
}

// applySessionOptions sets config options that are fixed for the whole session
// rather than chosen per turn. See Config.LaunchSessionOptions.
func (c *conversation) applySessionOptions(ctx context.Context, options []SessionOption) error {
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	if sessionID == "" {
		return errors.New("ACP session is not open")
	}
	for _, option := range options {
		if err := c.setSessionOption(ctx, sessionID, option); err != nil {
			return err
		}
	}
	return nil
}

// awaitConfigOption reports whether the session advertises option's id and
// offers its value, waiting up to optionCatalogWait for an agent that publishes
// part of its catalog after session/new. The wait ends as soon as ctx is
// cancelled, returning ctx's error.
func (c *conversation) awaitConfigOption(ctx context.Context, option SessionOption) (offered, known bool, err error) {
	offered, known = c.configOptionOffers(option.ID, option.Value)
	if known || c.optionCatalogWait <= 0 {
		return offered, known, ctx.Err()
	}
	timeout := time.NewTimer(c.optionCatalogWait)
	defer timeout.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for !known {
		select {
		case <-ctx.Done():
			return false, false, ctx.Err()
		case <-timeout.C:
			return offered, known, nil
		case <-tick.C:
			offered, known = c.configOptionOffers(option.ID, option.Value)
		}
	}
	return offered, known, nil
}

// setSessionOption sends one session/set_config_option and adopts any catalog
// the agent returns, preserving the current catalog when it omits one.
func (c *conversation) setSessionOption(ctx context.Context, sessionID string, option SessionOption) error {
	if option.ID == "" || option.Value == "" {
		return nil
	}
	c.mu.Lock()
	requireAdvertised := c.requireAdvertised
	c.mu.Unlock()
	if requireAdvertised {
		// An agent that acknowledges an option it never offered may simply drop
		// it, which would leave the user believing a choice took effect.
		offered, known, err := c.awaitConfigOption(ctx, option)
		if err != nil {
			return err
		}
		if !known {
			return fmt.Errorf("%w: ACP session does not advertise option %q",
				ports.ErrChatConfigOptionInvalid, option.ID)
		}
		if !offered {
			return fmt.Errorf("%w: ACP session option %q does not offer %q",
				ports.ErrChatConfigOptionInvalid, option.ID, option.Value)
		}
	}
	resp, err := c.conn.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: acpsdk.SessionId(sessionID), ConfigId: acpsdk.SessionConfigId(option.ID),
			Value: acpsdk.SessionConfigValueId(option.Value),
		},
	})
	if err != nil {
		// An optional setting the chosen model lacks is skipped; session/set_model
		// does not return the new model's options to check in advance.
		if option.Optional && isACPInvalidParams(err) {
			return nil
		}
		if isACPMethodNotFound(err) {
			return fmt.Errorf("%w: session/set_config_option %q", ErrACPSetterUnsupported, option.ID)
		}
		return fmt.Errorf("set ACP session option %q: %w", option.ID, err)
	}
	if len(resp.ConfigOptions) == 0 {
		c.applyAcceptedConfigOption(option.ID, ports.ChatConfigOptionValue{Select: option.Value})
	} else {
		c.replaceConfigOptions(resp.ConfigOptions)
	}
	return nil
}
