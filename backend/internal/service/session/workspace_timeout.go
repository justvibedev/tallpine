package session

import "time"

// workspaceReviewTimeout bounds one git review call. A var so tests can widen
// it on a loaded machine; production keeps 15s.
var workspaceReviewTimeout = 15 * time.Second
