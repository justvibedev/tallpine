package controllers

import "tallpine.eu/tallpine/backend/internal/httpd/apitypes"

// Wire types shared with the CLI live in httpd/apitypes; these aliases keep
// the controller API names stable.

// BackgroundTaskPayload aliases apitypes.BackgroundTaskPayload.
type BackgroundTaskPayload = apitypes.BackgroundTaskPayload

// SessionView aliases apitypes.SessionView.
type SessionView = apitypes.SessionView

// SessionArtifactView aliases apitypes.SessionArtifactView.
type SessionArtifactView = apitypes.SessionArtifactView

// ListSessionsResponse aliases apitypes.ListSessionsResponse.
type ListSessionsResponse = apitypes.ListSessionsResponse

// SessionArchiveSummary aliases apitypes.SessionArchiveSummary.
type SessionArchiveSummary = apitypes.SessionArchiveSummary

// SessionResponse aliases apitypes.SessionResponse.
type SessionResponse = apitypes.SessionResponse

// SessionLastAssistantMessage aliases apitypes.SessionLastAssistantMessage.
type SessionLastAssistantMessage = apitypes.SessionLastAssistantMessage

// SessionPRFacts aliases apitypes.SessionPRFacts.
type SessionPRFacts = apitypes.SessionPRFacts

// SessionPRSummary aliases apitypes.SessionPRSummary.
type SessionPRSummary = apitypes.SessionPRSummary

// SessionPRCISummary aliases apitypes.SessionPRCISummary.
type SessionPRCISummary = apitypes.SessionPRCISummary

// SessionPRFailingCheck aliases apitypes.SessionPRFailingCheck.
type SessionPRFailingCheck = apitypes.SessionPRFailingCheck

// SessionPRReviewSummary aliases apitypes.SessionPRReviewSummary.
type SessionPRReviewSummary = apitypes.SessionPRReviewSummary

// SessionPRReviewEntry aliases apitypes.SessionPRReviewEntry.
type SessionPRReviewEntry = apitypes.SessionPRReviewEntry

// SessionPRUnresolvedReviewer aliases apitypes.SessionPRUnresolvedReviewer.
type SessionPRUnresolvedReviewer = apitypes.SessionPRUnresolvedReviewer

// SessionPRReviewCommentLink aliases apitypes.SessionPRReviewCommentLink.
type SessionPRReviewCommentLink = apitypes.SessionPRReviewCommentLink

// SessionPRMergeabilitySummary aliases apitypes.SessionPRMergeabilitySummary.
type SessionPRMergeabilitySummary = apitypes.SessionPRMergeabilitySummary

// SessionPRConflictFile aliases apitypes.SessionPRConflictFile.
type SessionPRConflictFile = apitypes.SessionPRConflictFile

// SessionPRReference aliases apitypes.SessionPRReference.
type SessionPRReference = apitypes.SessionPRReference

// ListSessionPRsResponse aliases apitypes.ListSessionPRsResponse.
type ListSessionPRsResponse = apitypes.ListSessionPRsResponse

// ClaimPRRequest aliases apitypes.ClaimPRRequest.
type ClaimPRRequest = apitypes.ClaimPRRequest

// ClaimPRResponse aliases apitypes.ClaimPRResponse.
type ClaimPRResponse = apitypes.ClaimPRResponse

// RenameSessionRequest aliases apitypes.RenameSessionRequest.
type RenameSessionRequest = apitypes.RenameSessionRequest

// RenameSessionResponse aliases apitypes.RenameSessionResponse.
type RenameSessionResponse = apitypes.RenameSessionResponse

// RestoreSessionResponse aliases apitypes.RestoreSessionResponse.
type RestoreSessionResponse = apitypes.RestoreSessionResponse

// ExitAgentResponse aliases apitypes.ExitAgentResponse.
type ExitAgentResponse = apitypes.ExitAgentResponse

// ResumeAgentResponse aliases apitypes.ResumeAgentResponse.
type ResumeAgentResponse = apitypes.ResumeAgentResponse

// KillSessionResponse aliases apitypes.KillSessionResponse.
type KillSessionResponse = apitypes.KillSessionResponse

// CleanupSkippedSession aliases apitypes.CleanupSkippedSession.
type CleanupSkippedSession = apitypes.CleanupSkippedSession

// CleanupSessionsResponse aliases apitypes.CleanupSessionsResponse.
type CleanupSessionsResponse = apitypes.CleanupSessionsResponse
