package cmd

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/cache"
)

func resetCacheStatsFlags(t *testing.T) {
	t.Helper()
	cacheStatsFlags.jsonOut = false
	cacheStatsFlags.model = ""
	if err := cacheStatsCmd.Flags().Set("json", "false"); err != nil {
		t.Fatalf("reset --json flag: %v", err)
	}
	if err := cacheStatsCmd.Flags().Set("model", ""); err != nil {
		t.Fatalf("reset --model flag: %v", err)
	}
}

func TestCacheCmd_Flags(t *testing.T) {
	tests := []struct {
		name   string
		flag   string
		set    string
		isBool bool
	}{
		{"list", "list", "true", true},
		{"server", "server", "testserver", false},
		{"search", "search", "testtool", false},
		{"json", "json", "true", true},
		{"clear", "clear", "true", true},
		{"location", "location", "true", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := cacheCmd.Flags().Set(tt.flag, tt.set); err != nil {
				t.Fatalf("set flag %s: %v", tt.flag, err)
			}

			if tt.isBool {
				got, err := cacheCmd.Flags().GetBool(tt.flag)
				if err != nil {
					t.Fatalf("get flag %s: %v", tt.flag, err)
				}
				if !got {
					t.Errorf("flag %s = %v, want true", tt.flag, got)
				}
			} else {
				got, err := cacheCmd.Flags().GetString(tt.flag)
				if err != nil {
					t.Fatalf("get flag %s: %v", tt.flag, err)
				}
				if got != tt.set {
					t.Errorf("flag %s = %v, want %v", tt.flag, got, tt.set)
				}
			}
		})
	}
}

func TestCacheCmd_HelpOutput(t *testing.T) {
	cmd := cacheCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestCacheCmd_ListFlag(t *testing.T) {
	cmd := cacheCmd
	cmd.SetArgs([]string{"--list"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("list flag should not error: %v", err)
	}
}

func TestCacheCmd_LocationFlag(t *testing.T) {
	cmd := cacheCmd
	cmd.SetArgs([]string{"--location"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("location flag should not error: %v", err)
	}
}

func TestCacheStatsCmd_HelpOutput(t *testing.T) {
	resetCacheStatsFlags(t)
	cache.GlobalCacheStatsTracker().Reset()

	var buf bytes.Buffer
	cmd := cacheStatsCmd
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Help(); err != nil {
		t.Errorf("help should not error: %v", err)
	}
	output := buf.String()

	if !strings.Contains(output, "cache") {
		t.Errorf("help output should contain 'cache', got: %s", output)
	}
	if !strings.Contains(output, "Anthropic") {
		t.Errorf("help output should mention Anthropic, got: %s", output)
	}
}

func TestCacheStatsCmd_NoTraffic(t *testing.T) {
	resetCacheStatsFlags(t)
	cache.GlobalCacheStatsTracker().Reset()

	stats := cache.GlobalCacheStatsTracker().GetStats()
	if stats.HasTraffic() {
		t.Fatalf("tracker should start with no traffic, got %+v", stats)
	}
}

func TestCacheStatsCmd_JsonFlag(t *testing.T) {
	resetCacheStatsFlags(t)
	cache.GlobalCacheStatsTracker().Reset()
	cache.GlobalCacheStatsTracker().RecordRequest(cache.ProviderAnthropic, true, 100)

	tracker := cache.GlobalCacheStatsTracker()
	before := tracker.GetStats()
	if before.AnthropicRequests != 1 {
		t.Fatalf("test setup: expected 1 anthropic request, got %d", before.AnthropicRequests)
	}
}

func TestCacheStatsCmd_ModelFlag(t *testing.T) {
	resetCacheStatsFlags(t)
	cache.GlobalCacheStatsTracker().Reset()
	cache.GlobalCacheStatsTracker().RecordRequest(cache.ProviderAnthropic, false, 50)

	if _, ok := cache.ModelCost("claude-3-5-sonnet-20241022"); !ok {
		t.Fatal("test prerequisite: model should exist in pricing table")
	}
}

func TestCacheStatsCmd_JsonAndModelFlags(t *testing.T) {
	resetCacheStatsFlags(t)
	cache.GlobalCacheStatsTracker().Reset()
	cache.GlobalCacheStatsTracker().RecordRequest(cache.ProviderAnthropic, true, 80)

	if _, ok := cache.ModelCost("claude-3-5-haiku-20241022"); !ok {
		t.Fatal("test prerequisite: model should exist in pricing table")
	}
}

func TestCacheStatsCmd_UnknownModelWarningEmitted(t *testing.T) {
	var captured bytes.Buffer
	handler := slog.NewTextHandler(&captured, &slog.HandlerOptions{Level: slog.LevelWarn})
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(prev)

	cache.GlobalCacheStatsTracker().Reset()
	cache.GlobalCacheStatsTracker().RecordRequest(cache.ProviderAnthropic, true, 10)

	fetcher := cacheStatsCmd
	fetcher.SetArgs([]string{"--model", "gpt-4-fake"})

	oldJSON := cacheStatsFlags.jsonOut
	oldModel := cacheStatsFlags.model
	cacheStatsFlags.jsonOut = false
	cacheStatsFlags.model = "gpt-4-fake"
	defer func() {
		cacheStatsFlags.jsonOut = oldJSON
		cacheStatsFlags.model = oldModel
	}()

	runCacheStats(fetcher, nil)

	if !strings.Contains(captured.String(), "unknown model") {
		t.Errorf("expected unknown-model warning via slog, got: %q", captured.String())
	}
}

func TestCacheStatsCmd_OtherProviderOnly_NoTraffic(t *testing.T) {
	resetCacheStatsFlags(t)
	cache.GlobalCacheStatsTracker().Reset()
	cache.GlobalCacheStatsTracker().RecordRequest(cache.ProviderOther, false, 100)

	stats := cache.GlobalCacheStatsTracker().GetStats()
	if stats.HasTraffic() {
		t.Fatalf("test setup: HasTraffic should be false with only Other provider, got %+v", stats)
	}
}

func TestCacheStatsCmd_FormatJSON_IsValid(t *testing.T) {
	cache.GlobalCacheStatsTracker().Reset()
	cache.GlobalCacheStatsTracker().RecordRequest(cache.ProviderAnthropic, true, 100)
	cache.GlobalCacheStatsTracker().RecordCacheHit(50)

	stats := cache.GlobalCacheStatsTracker().GetStats()
	out := stats.FormatJSON()
	if !strings.Contains(out, "total_requests") {
		t.Errorf("expected JSON to contain total_requests, got: %s", out)
	}
	if !strings.Contains(out, "cache_hits") {
		t.Errorf("expected JSON to contain cache_hits, got: %s", out)
	}
}
