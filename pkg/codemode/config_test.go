package codemode

import (
	"testing"
	"time"
)

func TestConfig(t *testing.T) {
	var nilCfg *Config
	if nilCfg.IsEnabled() || nilCfg.Validate() != nil || nilCfg.Limits().MaxCalls != DefaultMaxCalls {
		t.Fatal("nil config: off, valid, defaults")
	}
	c := &Config{Enabled: true, Timeout: "5s", CPUTime: "2s", MaxMemoryMB: 96, MaxCalls: 3, MaxConcurrentCalls: 1, MaxOutputBytes: 100}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	l := c.Limits()
	if l.Timeout != 5*time.Second || l.CPUTime != 2*time.Second || l.MaxMemoryMB != 96 || l.MaxCalls != 3 || l.MaxConcurrentCalls != 1 || l.MaxOutputBytes != 100 {
		t.Fatalf("limits %+v", l)
	}
	for _, bad := range []*Config{
		{Timeout: "x"}, {Timeout: "-1s"}, {Timeout: "1h"}, {CPUTime: "500ms"},
		{MaxMemoryMB: 10}, {MaxCalls: -1}, {MaxOutputBytes: -5},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v must be invalid", *bad)
		}
	}
}
