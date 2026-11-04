// Fake conversations that serve native provider history.
package chat_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"tallpine.eu/tallpine/backend/internal/ports"
)

type nativeHistoryConversation struct {
	*fakeConversation
	events []ports.ChatEvent
	err    error
	reads  atomic.Int32
	onRead func(int)
}

// rejectedHistoryConversation is an ACP-like reader whose provider refuses
// every session/load replay after an unsettled first observation.
type rejectedHistoryConversation struct {
	*fakeConversation
	refreshes atomic.Int32
}

func (c *rejectedHistoryConversation) ReadHistory(context.Context) ([]ports.ChatEvent, error) {
	return nil, ports.ErrChatHistoryUnsettled
}

func (c *rejectedHistoryConversation) RefreshHistory(context.Context) ([]ports.ChatEvent, error) {
	c.refreshes.Add(1)
	return nil, fmt.Errorf("refresh ACP session history: %w", ports.ErrChatHistoryLoadFailed)
}

// stalledHistoryConversation is an ACP-like reader whose provider never answers
// the session/load refresh; only context cancellation ends the call.
type stalledHistoryConversation struct {
	*fakeConversation
	refreshes      atomic.Int32
	refreshCtxDone atomic.Bool
}

func (c *stalledHistoryConversation) ReadHistory(context.Context) ([]ports.ChatEvent, error) {
	return nil, ports.ErrChatHistoryUnsettled
}

func (c *stalledHistoryConversation) RefreshHistory(ctx context.Context) ([]ports.ChatEvent, error) {
	c.refreshes.Add(1)
	<-ctx.Done()
	c.refreshCtxDone.Store(true)
	return nil, fmt.Errorf("refresh ACP session history: %w", ctx.Err())
}

type convergingHistoryConversation struct {
	*fakeConversation
	mu             sync.Mutex
	reads          int
	refreshes      int
	initialSettled bool
	initialEvents  []ports.ChatEvent
	events         []ports.ChatEvent
	onRead         func()
}

type blockingHistoryConversation struct {
	*fakeConversation
	started chan struct{}
	release chan struct{}
}

func (c *blockingHistoryConversation) ReadHistory(ctx context.Context) ([]ports.ChatEvent, error) {
	close(c.started)
	select {
	case <-c.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *nativeHistoryConversation) ReadHistory(context.Context) ([]ports.ChatEvent, error) {
	reads := int(c.reads.Add(1))
	if c.onRead != nil {
		c.onRead(reads)
	}
	return c.events, c.err
}

func (c *convergingHistoryConversation) ReadHistory(context.Context) ([]ports.ChatEvent, error) {
	c.mu.Lock()
	c.reads++
	reads := c.reads
	onRead := c.onRead
	initialSettled := c.initialSettled
	initialEvents := append([]ports.ChatEvent(nil), c.initialEvents...)
	events := append([]ports.ChatEvent(nil), c.events...)
	c.mu.Unlock()
	if onRead != nil {
		onRead()
	}
	if reads == 1 {
		if initialSettled {
			return initialEvents, nil
		}
		return nil, ports.ErrChatHistoryUnsettled
	}
	return events, nil
}

func (c *convergingHistoryConversation) RefreshHistory(context.Context) ([]ports.ChatEvent, error) {
	c.mu.Lock()
	c.refreshes++
	events := append([]ports.ChatEvent(nil), c.events...)
	c.mu.Unlock()
	return events, nil
}

func (c *convergingHistoryConversation) historyAttempts() (reads, refreshes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads, c.refreshes
}

func (c *nativeHistoryConversation) historyReads() int {
	return int(c.reads.Load())
}
