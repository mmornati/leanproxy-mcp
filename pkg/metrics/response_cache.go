package metrics

import "sync/atomic"

// ResponseCacheMetric is the response cache's (issue #299) counters as
// exposed on the /metrics endpoint. It deliberately mirrors
// responsecache.Stats field-for-field instead of importing pkg/mcp, so this
// package stays a leaf dependency.
type ResponseCacheMetric struct {
	Enabled   bool   `json:"enabled"`
	Hits      uint64 `json:"hits"`
	Misses    uint64 `json:"misses"`
	Evictions uint64 `json:"evictions"`
	Bytes     int64  `json:"bytes"`
	Entries   int    `json:"entries"`
}

var responseCacheProvider atomic.Pointer[func() ResponseCacheMetric]

// SetResponseCacheProvider registers the function Snapshot uses to populate
// ResponseCache. Front ends (`server run --stdio`, `serve`) call this once
// at startup with a closure over their own *mcp.ResponseCache. Passing nil
// clears it, so ResponseCache is omitted from the snapshot again.
func SetResponseCacheProvider(f func() ResponseCacheMetric) {
	if f == nil {
		responseCacheProvider.Store(nil)
		return
	}
	responseCacheProvider.Store(&f)
}

func responseCacheSnapshot() *ResponseCacheMetric {
	p := responseCacheProvider.Load()
	if p == nil {
		return nil
	}
	m := (*p)()
	return &m
}
