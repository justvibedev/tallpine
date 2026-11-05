package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type sendOptions struct {
	session         string
	message         string
	steer           bool
	clientMessageID string
	recoverOnly     bool
	relayOwner      string
}

func newSendCommand(ctx *commandContext) *cobra.Command {
	var opts sendOptions
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a message to a running agent session",
		Long: `Send a message to a running agent session and report what happened to it:
"delivered" (written to the session; for a terminal that does not prove the
agent has read it), "queued for next turn" (accepted, but the
agent will only see it after its current turn or an interface transition) or
"duplicate" (a message with that id was already accepted), with a delivery id
when one exists. A message the agent cannot take right now is
refused with an error instead of being silently dropped.

--relay-owner-message passes on a message the owner typed in your own chat
(its id, or "last" for the newest one). Tallpine checks it is the owner's own
turn and delivers it verbatim in a daemon-stamped block the receiver can
trust; your --message text stays your own, untrusted words. It is refused
when the message is not one the owner typed.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.sendMessage(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "Session id (required)")
	cmd.Flags().StringVar(&opts.message, "message", "", "Message body (required unless --recover-only)")
	cmd.Flags().BoolVar(&opts.steer, "steer", false, "Steer an active Chat turn, or start the next turn when idle")
	cmd.Flags().StringVar(&opts.clientMessageID, "client-message-id", "", "Stable steering delivery handle")
	cmd.Flags().BoolVar(&opts.recoverOnly, "recover-only", false, "Recover a steering result without contacting the provider")
	cmd.Flags().StringVar(&opts.relayOwner, "relay-owner-message", "", "Relay an owner message from your own chat, verified by Tallpine (id, or last)")
	return cmd
}

func (c *commandContext) sendMessage(ctx context.Context, opts sendOptions) error {
	if opts.recoverOnly && !opts.steer {
		return usageError{errors.New("usage: --recover-only requires --steer")}
	}
	if strings.TrimSpace(opts.clientMessageID) != "" && !opts.steer {
		return usageError{errors.New("usage: --client-message-id requires --steer")}
	}
	if opts.recoverOnly && strings.TrimSpace(opts.clientMessageID) == "" {
		return usageError{errors.New("usage: --recover-only requires --client-message-id")}
	}
	if strings.TrimSpace(opts.relayOwner) != "" && opts.steer {
		return usageError{errors.New("usage: --relay-owner-message cannot be combined with --steer")}
	}
	if !opts.recoverOnly && strings.TrimSpace(opts.message) == "" {
		return usageError{errors.New("usage: --message is required")}
	}
	message := opts.message
	if sender := strings.TrimSpace(brand.Getenv("SESSION_ID")); !opts.recoverOnly && sender != "" {
		message = "[from " + sender + "] " + message
	}
	session := strings.TrimSpace(opts.session)
	if session == "" {
		return usageError{errors.New("usage: --session is required")}
	}

	// PathEscape: session ids are already "-"/digit safe, but may later come
	// from sanitized issue refs; keep the URL well-formed regardless.
	path := "sessions/" + url.PathEscape(session)
	if !opts.steer {
		var res apitypes.SendSessionMessageResponse
		if err := c.postJSON(ctx, path+"/send", apitypes.SendSessionMessageRequest{Message: message, SenderSessionID: strings.TrimSpace(brand.Getenv("SESSION_ID")), UserAuthored: strings.TrimSpace(brand.Getenv("SESSION_ID")) == "", RelayOwnerMessage: strings.TrimSpace(opts.relayOwner)}, &res); err != nil {
			return err
		}
		_, err := fmt.Fprintln(c.deps.Out, describeSendOutcome(session, res))
		return err
	}
	return c.steerMessage(ctx, path, message, strings.TrimSpace(opts.clientMessageID), opts.recoverOnly)
}

func (c *commandContext) steerMessage(
	ctx context.Context,
	sessionPath, message, clientMessageID string,
	recoverOnly bool,
) error {
	if clientMessageID == "" {
		clientMessageID = uuid.NewString()
	}
	var result apitypes.SteerOrSendConversationResponse
	err := c.postJSON(ctx, sessionPath+"/conversation/steer-or-send", apitypes.SteerConversationRequest{
		Text: message, ClientMessageID: clientMessageID, SenderSessionID: strings.TrimSpace(brand.Getenv("SESSION_ID")), RecoverOnly: recoverOnly,
	}, &result)
	if err != nil {
		var responseErr apiResponseError
		hasResponseErr := errors.As(err, &responseErr)
		if hasResponseErr && responseErr.ErrorBody.Code == "CHAT_STEER_UNCERTAIN" {
			if recoverOnly {
				return fmt.Errorf("%w; delivery handle %s remains unresolved and was not resent", err, clientMessageID)
			}
			return fmt.Errorf(
				"%w; recover this delivery without resending it: tallpine send --session %s --steer --recover-only --client-message-id %s",
				err, strings.TrimPrefix(sessionPath, "sessions/"), clientMessageID)
		}
		if errors.Is(err, errDaemonUnavailable) || !hasResponseErr || responseErr.StatusCode >= 500 {
			return fmt.Errorf(
				"%w; outcome is unknown for delivery handle %s; retry safely with --client-message-id %s",
				err, clientMessageID, clientMessageID)
		}
		return err
	}

	if result.Outcome == "steered" {
		if recoverOnly {
			_, _ = fmt.Fprintf(c.deps.Out,
				"Recovered steering receipt for turn %s with delivery handle %s; agent action is not confirmed.\n",
				result.ProviderTurnID, clientMessageID)
			return nil
		}
		_, _ = fmt.Fprintf(c.deps.Out,
			"Steering accepted by provider for active turn %s with delivery handle %s; agent action is not confirmed.\n",
			result.ProviderTurnID, clientMessageID)
		return nil
	}
	if result.Duplicate {
		_, _ = fmt.Fprintln(c.deps.Out, "Message was already accepted; delivery state is unchanged.")
		return nil
	}
	_, _ = fmt.Fprintf(c.deps.Out,
		"No active turn to steer. Message accepted as a normal Chat turn in %s state with delivery handle %s; provider delivery is not confirmed.\n",
		result.State, clientMessageID)
	return nil
}

// describeSendOutcome tells the caller whether the message reached the agent or
// is waiting for it, so a send to a busy session is not mistaken for delivery.
func describeSendOutcome(session string, res apitypes.SendSessionMessageResponse) string {
	handle := ""
	if res.DeliveryID != "" {
		handle = " (id " + res.DeliveryID + ")"
	}
	switch res.Outcome {
	case "delivered":
		if res.WaitingOnInput {
			return "delivered to " + session + handle + " into its running turn, which was waiting on an in-chat question"
		}
		return "delivered to " + session + handle
	case "duplicate":
		return "not sent to " + session + ": a message with this id was already accepted; its delivery state is not reported"
	case "queued":
		if res.WaitingOnInput {
			return "queued for next turn in " + session + handle + "; NOT delivered yet: its running turn is waiting on an in-chat question, so the agent sees this only after that question is answered in the app"
		}
		return "queued for next turn in " + session + handle + "; the agent will see it after its current work"
	default:
		return "sent to " + session + "; delivery outcome not reported by the daemon"
	}
}
