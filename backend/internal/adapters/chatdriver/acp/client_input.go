package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// UnstableCreateElicitation bridges ACP's structured input request into Tallpine's
// ordinary durable conversation/event path. The JSON-RPC call remains parked
// until a client answers, exactly like a permission request, but it has a
// separate response contract so form data can never be mistaken for consent.
func (c *conversation) UnstableCreateElicitation(
	ctx context.Context,
	params acpsdk.UnstableCreateElicitationRequest,
) (acpsdk.UnstableCreateElicitationResponse, error) {
	request := ports.ChatInputRequest{}
	switch {
	case params.Form != nil:
		request.Mode = ports.ChatInputModeForm
		request.Message = params.Form.Message
		schema, err := schemaMap(params.Form.RequestedSchema)
		if err != nil {
			return acpsdk.NewUnstableCreateElicitationResponseCancel(), err
		}
		request.Schema = schema
	case params.Url != nil:
		request.Mode = ports.ChatInputModeURL
		request.Message = params.Url.Message
		request.URL = params.Url.Url
		request.ElicitationID = string(params.Url.ElicitationId)
	default:
		return acpsdk.NewUnstableCreateElicitationResponseCancel(), errors.New("ACP elicitation has no mode")
	}

	requestID := ""
	if params.Form != nil {
		requestID = stableACPRequestID(params.Form.Meta)
	} else if params.Url != nil {
		requestID = stableACPRequestID(params.Url.Meta)
	}
	response, err := c.requestInput(ctx, requestID, request)
	if err != nil {
		return acpsdk.NewUnstableCreateElicitationResponseCancel(), err
	}
	return acpInputResponse(response), nil
}

// RequestInput parks a provider extension on Tallpine's durable structured-input flow.
func (c *conversation) RequestInput(
	ctx context.Context,
	request ports.ChatInputRequest,
) (ports.ChatInputResponse, error) {
	return c.requestInput(ctx, "", request)
}

func (c *conversation) requestInput(
	ctx context.Context,
	requestID string,
	request ports.ChatInputRequest,
) (ports.ChatInputResponse, error) {
	if requestID == "" {
		requestID = uuid.NewString()
	}
	parked := &parkedInput{
		request: request, result: make(chan ports.ChatInputResponse, 1), ready: make(chan struct{}),
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ports.ChatInputResponse{Action: ports.ChatInputActionCancel}, nil
	}
	accepted, hasAccepted := c.accepted[requestID]
	if hasAccepted && accepted.Kind == persistentInteractionInput {
		delete(c.accepted, requestID)
	} else {
		c.pendingInputs[requestID] = parked
	}
	turnID := c.activeTurn
	c.mu.Unlock()

	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventInputRequested, ProviderTurnID: turnID,
		RequestID: requestID, Input: &request, Summary: request.Message,
	})
	close(parked.ready)
	if hasAccepted && accepted.Kind == persistentInteractionInput {
		c.emit(persistentInteractionEvent(accepted))
		parked.result <- *accepted.Input
	}

	timer := timeAfter(approvalWait)
	select {
	case response := <-parked.result:
		return response, nil
	case <-ctx.Done():
		c.discardInput(requestID)
		if !c.providerDetaching() {
			c.emit(ports.ChatEvent{Kind: ports.ChatEventInputResolved, RequestID: requestID})
		}
		return ports.ChatInputResponse{Action: ports.ChatInputActionCancel}, nil
	case <-timer:
		c.discardInput(requestID)
		c.emit(ports.ChatEvent{Kind: ports.ChatEventInputResolved, RequestID: requestID})
		return ports.ChatInputResponse{Action: ports.ChatInputActionCancel}, nil
	}
}

func stableACPRequestID(meta map[string]any) string {
	requestID, _ := meta[persistenthost.ACPRequestIDMetaKey].(string)
	return strings.TrimSpace(requestID)
}

func (c *conversation) ResolveInput(
	ctx context.Context,
	requestID string,
	response ports.ChatInputResponse,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	request, ok := c.pendingInputs[requestID]
	if !ok {
		c.mu.Unlock()
		return ports.ErrChatRequestNotPending
	}
	if err := validateInputResponse(request.request, response); err != nil {
		c.mu.Unlock()
		return err
	}
	delete(c.pendingInputs, requestID)
	c.mu.Unlock()
	command := persistentInteractionCommand{
		RequestID: requestID, Kind: persistentInteractionInput, Input: &response,
	}
	eventID, err := c.recordPersistentInteraction(ctx, command)
	if err != nil {
		c.mu.Lock()
		if !c.closed {
			c.pendingInputs[requestID] = request
		}
		c.mu.Unlock()
		return err
	}
	command.EventID = eventID

	c.emit(persistentInteractionEvent(command))
	request.result <- response
	return nil
}

func (c *conversation) UnstableCompleteElicitation(
	context.Context,
	acpsdk.UnstableCompleteElicitationNotification,
) error {
	// URL completion describes provider-side progress after the user has already
	// consented. The actionable Tallpine request was resolved when that consent was sent.
	return nil
}

func (c *conversation) UnstableConnectMcp(
	context.Context,
	acpsdk.UnstableConnectMcpRequest,
) (acpsdk.UnstableConnectMcpResponse, error) {
	return acpsdk.UnstableConnectMcpResponse{}, errClientCapability
}

func (c *conversation) UnstableDisconnectMcp(
	context.Context,
	acpsdk.UnstableDisconnectMcpRequest,
) (acpsdk.UnstableDisconnectMcpResponse, error) {
	return acpsdk.UnstableDisconnectMcpResponse{}, errClientCapability
}

func schemaMap(schema acpsdk.UnstableElicitationSchema) (map[string]any, error) {
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode ACP elicitation schema: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, fmt.Errorf("normalize ACP elicitation schema: %w", err)
	}
	return out, nil
}

func acpInputResponse(response ports.ChatInputResponse) acpsdk.UnstableCreateElicitationResponse {
	switch response.Action {
	case ports.ChatInputActionAccept:
		result := acpsdk.NewUnstableCreateElicitationResponseAccept()
		result.Accept.Content = response.Content
		return result
	case ports.ChatInputActionDecline:
		return acpsdk.NewUnstableCreateElicitationResponseDecline()
	default:
		return acpsdk.NewUnstableCreateElicitationResponseCancel()
	}
}

func validateInputResponse(request ports.ChatInputRequest, response ports.ChatInputResponse) error {
	switch response.Action {
	case ports.ChatInputActionDecline, ports.ChatInputActionCancel:
		return nil
	case ports.ChatInputActionAccept:
		if request.Mode == ports.ChatInputModeURL {
			return nil
		}
	default:
		return fmt.Errorf("%w: unsupported input action %q", ports.ErrChatDecisionNotOffered, response.Action)
	}
	if request.Mode != ports.ChatInputModeForm {
		return fmt.Errorf("%w: unsupported input mode %q", ports.ErrChatDecisionNotOffered, request.Mode)
	}
	if err := validateFormContent(request.Schema, response.Content); err != nil {
		return fmt.Errorf("%w: %s", ports.ErrChatDecisionNotOffered, err.Error())
	}
	return nil
}

func validateFormContent(schema, content map[string]any) error {
	properties, _ := schema["properties"].(map[string]any)
	customAnswers := formCustomAnswers(properties)
	for _, name := range formRequired(schema["required"]) {
		if name == "" {
			continue
		}
		if _, present := content[name]; present {
			continue
		}
		// A typed "Other" answer stands in for the choice it replaces; the adapter
		// records it as the question's answer.
		if custom, ok := customAnswers[name]; ok {
			if text, _ := content[custom].(string); strings.TrimSpace(text) != "" {
				continue
			}
		}
		return fmt.Errorf("required input %q is missing", name)
	}
	for name, value := range content {
		rawProperty, known := properties[name]
		if !known {
			return fmt.Errorf("input %q is not in the requested schema", name)
		}
		property, ok := rawProperty.(map[string]any)
		if !ok {
			return fmt.Errorf("input %q has an invalid requested schema", name)
		}
		if err := validateFormValue(value, property); err != nil {
			return fmt.Errorf("input %q %s", name, err.Error())
		}
	}
	return nil
}

func validateFormValue(value any, property map[string]any) error {
	typeName, _ := property["type"].(string)
	switch typeName {
	case "string", "":
		text, ok := value.(string)
		if !ok {
			return errors.New("must be a string")
		}
		if !formOptionOffered(text, property) {
			return errors.New("is not one of the offered values")
		}
	case "number", "integer":
		numeric, ok := number(value)
		if !ok || math.IsNaN(numeric) || math.IsInf(numeric, 0) {
			return errors.New("must be a finite number")
		}
		if typeName == "integer" && math.Trunc(numeric) != numeric {
			return errors.New("must be an integer")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return errors.New("must be a boolean")
		}
	case "array":
		values, ok := value.([]any)
		if !ok {
			return errors.New("must be an array")
		}
		items, _ := property["items"].(map[string]any)
		for _, item := range values {
			text, ok := item.(string)
			if !ok || !formOptionOffered(text, items) {
				return errors.New("contains a value that was not offered")
			}
		}
	default:
		return fmt.Errorf("uses unsupported type %q", typeName)
	}
	return nil
}

// askUserQuestionCustomAnswerMetaKey marks the free-text "Other" field an
// AskUserQuestion bridge (claude-agent-acp) adds after each question, naming the
// question it answers. Only this marker makes a field an alternative answer, so a
// generic form's required fields stay strictly required.
const askUserQuestionCustomAnswerMetaKey = "_askUserQuestionCustomAnswer"

// formCustomAnswers maps each marked question to the field that answers it in free text.
func formCustomAnswers(properties map[string]any) map[string]string {
	var out map[string]string
	for name, raw := range properties {
		property, _ := raw.(map[string]any)
		meta, _ := property["_meta"].(map[string]any)
		marker, _ := meta[askUserQuestionCustomAnswerMetaKey].(map[string]any)
		question, _ := marker["questionId"].(string)
		if marker["isCustomAnswer"] != true || question == "" || question == name {
			continue
		}
		if _, known := properties[question]; !known {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[question] = name
	}
	return out
}

func formRequired(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func formOptionOffered(value string, schema map[string]any) bool {
	var options []any
	if candidates, ok := schema["oneOf"].([]any); ok {
		options = candidates
	} else if candidates, ok := schema["anyOf"].([]any); ok {
		options = candidates
	} else if candidates, ok := schema["enum"].([]any); ok {
		for _, candidate := range candidates {
			if candidate == value {
				return true
			}
		}
		return len(candidates) == 0
	}
	if len(options) == 0 {
		return true
	}
	for _, raw := range options {
		option, _ := raw.(map[string]any)
		if option["const"] == value {
			return true
		}
	}
	return false
}

func (c *conversation) discardInput(requestID string) {
	c.mu.Lock()
	delete(c.pendingInputs, requestID)
	c.mu.Unlock()
}
