package controllers

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

func (c *SessionsController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}")
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := SessionResponse{Session: c.view(r.Context(), r, sess)}
	if c.ServedModel != nil && !resp.Session.IsTerminated {
		if m := c.ServedModel(r.Context(), sess); m != "" {
			resp.Session.Model = m
		}
	}
	if resp.Session.Model == "" && c.Models != nil && !resp.Session.IsTerminated {
		resp.Session.Model = c.Models.EffectiveModel(r.Context(), sess.ID)
	}
	if c.LastMessages != nil {
		// A failed read leaves the optional field out; the session is still served.
		if text, at, found, readErr := c.LastMessages.LastAssistantMessage(r.Context(), sess.ID); readErr == nil && found {
			resp.LastAssistantMessage = &SessionLastAssistantMessage{Text: text, At: at}
		}
	}
	if c.Instructions != nil {
		// Like the last message, a failed read only omits the optional field.
		if rules, readErr := c.Instructions.WorkerInstructions(r.Context(), sess.ID); readErr == nil {
			resp.WorkerInstructions = rules
		}
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}
