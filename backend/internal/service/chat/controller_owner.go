package chat

import "tallpine.eu/tallpine/backend/internal/domain"

func reviewOwnerID(owner domain.ConversationOwner) string {
	if owner.Kind == domain.ConversationOwnerReview {
		return owner.ID
	}
	return ""
}

func (c *Controller) owner() domain.ConversationOwner {
	if c.reviewID != "" {
		return domain.ReviewConversationOwner(c.reviewID)
	}
	return domain.SessionConversationOwner(c.sessionID)
}
