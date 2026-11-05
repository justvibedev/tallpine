package chat_test

import "sync/atomic"

type stuckConversation struct {
	*fakeConversation
	closeErr error
}

func (s *stuckConversation) Close() error { return s.closeErr }

type terminatingConversation struct {
	*fakeConversation
	terminated atomic.Bool
}

func (c *terminatingConversation) Terminate() error {
	c.terminated.Store(true)
	return c.Close()
}

func (c *terminatingConversation) PreservesProviderOnClose() bool { return true }

type liveReconnectedConversation struct{ *nativeHistoryConversation }

func (c *liveReconnectedConversation) ReconnectedLive() bool { return true }
