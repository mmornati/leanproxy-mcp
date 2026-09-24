package metrics

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
)

// The response governor's accounting (#319) is exposed on /metrics only
// while it is enabled, and only as numbers.
func TestSnapshot_ResponseGovernor(t *testing.T) {
	t.Cleanup(func() { SetResponseGovernorProvider(nil) })

	SetResponseGovernorProvider(nil)
	if Snapshot().ResponseGovernor != nil {
		t.Fatal("no provider: response_governor must be omitted")
	}
	SetResponseGovernorProvider(func() mcp.GovernorStats { return mcp.GovernorStats{} })
	if Snapshot().ResponseGovernor != nil {
		t.Fatal("disabled governor: response_governor must be omitted")
	}
	SetResponseGovernorProvider(func() mcp.GovernorStats {
		return mcp.GovernorStats{Enabled: true, MaxTokens: 4000, Results: 3, Truncated: 1, OriginalTokens: 50000, ReturnedTokens: 4100, SavedTokens: 45900,
			ByTool: []mcp.GovernorToolStats{{Tool: "fs.read_file", Results: 1, Truncated: 1, OriginalTokens: 49000, ReturnedTokens: 3900}}}
	})
	snap := Snapshot()
	if snap.ResponseGovernor == nil || snap.ResponseGovernor.SavedTokens != 45900 {
		t.Fatalf("response_governor = %+v", snap.ResponseGovernor)
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"response_governor":{"enabled":true,"max_tokens":4000`) {
		t.Fatalf("JSON = %s", data)
	}
}
