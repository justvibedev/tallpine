package github

// cacheMaxEntries caps the number of distinct (method,path,query) tuples
// the in-memory ETag cache will track. A single Provider observes one PR
// at a time today, but the follow-up poller will reuse one Provider for
// the whole daemon — without a cap, long-running daemons would grow this
// map forever.
const cacheMaxEntries = 512

// storeCacheEntry records one (ETag, body) pair under cacheKey and evicts
// the oldest entry once cacheMaxEntries is exceeded. FIFO is intentional:
// the access pattern is "one PR per poll cycle"; an LRU would just add
// bookkeeping without changing eviction order in practice.
func (c *Client) storeCacheEntry(cacheKey, etag string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(body) > cacheMaxBytes {
		return // larger than the whole budget; an older smaller entry stays, harmless since a 304 implies unchanged content
	}
	// Membership is decided by bodyOut (kept in lockstep with cacheLRU), not
	// etagOut, which a late 304 could repopulate for an evicted key.
	if _, exists := c.bodyOut[cacheKey]; !exists {
		c.cacheLRU = append(c.cacheLRU, cacheKey)
	}
	c.cacheLen += len(body) - len(c.bodyOut[cacheKey])
	c.etagOut[cacheKey] = etag
	c.bodyOut[cacheKey] = body
	for (len(c.cacheLRU) > cacheMaxEntries || c.cacheLen > cacheMaxBytes) && len(c.cacheLRU) > 0 {
		evict := c.cacheLRU[0]
		c.cacheLRU = c.cacheLRU[1:]
		c.cacheLen -= len(c.bodyOut[evict])
		delete(c.etagOut, evict)
		delete(c.bodyOut, evict)
	}
}
