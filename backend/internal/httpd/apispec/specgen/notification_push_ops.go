package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

func notificationOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/notifications", id: "listNotifications", tag: "notifications",
			summary:    "List notification history",
			pathParams: []any{controllers.ListNotificationsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListNotificationsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/notifications/{id}", id: "markNotificationRead", tag: "notifications",
			summary:    "Mark a notification read",
			pathParams: []any{controllers.NotificationIDParam{}},
			reqBody:    controllers.MarkNotificationReadRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.NotificationEnvelope{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/notifications/{id}", id: "deleteNotification", tag: "notifications",
			summary:    "Delete a notification",
			pathParams: []any{controllers.NotificationIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.NotificationEnvelope{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/notifications/read-all", id: "markAllNotificationsRead", tag: "notifications",
			summary: "Mark notifications read",
			reqBody: controllers.MarkAllNotificationsReadRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.MarkAllNotificationsReadResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/notifications/stream", id: "streamNotifications", tag: "notifications",
			summary:    "Stream notification changes",
			pathParams: []any{controllers.NotificationStreamQuery{}},
			resps: []respUnit{
				{http.StatusOK, ""},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
			contentTypes: map[int]string{http.StatusOK: "text/event-stream"},
		},
		{
			method: http.MethodDelete, path: "/api/v1/notifications", id: "clearNotifications", tag: "notifications",
			summary: "Clear all notifications",
			resps: []respUnit{
				{http.StatusOK, controllers.ClearNotificationsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

// reviewOperations declares the session-scoped /reviews operations. Must stay
// 1:1 with the routes ReviewsController.Register mounts (enforced by the parity
// test).
// pushOperations declares the /push/devices operations. Must stay 1:1 with the
// routes PushController.Register mounts (enforced by the parity test).
func pushOperations() []operation {
	return []operation{
		{
			method: http.MethodPost, path: "/api/v1/push/devices", id: "registerPushDevice", tag: "push",
			summary: "Register (upsert) a phone's Expo push token",
			reqBody: controllers.RegisterPushDeviceRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.PushDeviceEnvelope{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/push/devices/{token}", id: "unregisterPushDevice", tag: "push",
			summary:    "Unregister a phone's Expo push token, leaving it paired",
			pathParams: []any{controllers.PushDeviceTokenParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.UnregisterPushDeviceResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/push/pairings/{id}", id: "unpairPushDevice", tag: "push",
			summary:    "Unpair this phone from the daemon, removing it from the roster",
			pathParams: []any{controllers.PushPairingIDParam{}},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}
