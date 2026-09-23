package responsecache

import "testing"

func TestConfig_Normalize_Defaults(t *testing.T) {
	c := &Config{}
	if err := c.Normalize(); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if c.TTLValue != DefaultTTL {
		t.Errorf("TTLValue = %v, want %v", c.TTLValue, DefaultTTL)
	}
	if c.MaxBytes != DefaultMaxBytes {
		t.Errorf("MaxBytes = %d, want %d", c.MaxBytes, DefaultMaxBytes)
	}
	if c.MaxEntryBytes != DefaultMaxEntryBytes {
		t.Errorf("MaxEntryBytes = %d, want %d", c.MaxEntryBytes, DefaultMaxEntryBytes)
	}
}

func TestConfig_Normalize_ExplicitValues(t *testing.T) {
	c := &Config{TTL: "10m", MaxBytes: 1024, MaxEntryBytes: 128}
	if err := c.Normalize(); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if c.TTLValue.String() != "10m0s" {
		t.Errorf("TTLValue = %v, want 10m0s", c.TTLValue)
	}
	if c.MaxBytes != 1024 || c.MaxEntryBytes != 128 {
		t.Errorf("MaxBytes/MaxEntryBytes not preserved: %+v", c)
	}
}

func TestConfig_Normalize_InvalidTTL(t *testing.T) {
	c := &Config{TTL: "not-a-duration"}
	if err := c.Normalize(); err == nil {
		t.Fatal("expected an error for an invalid ttl")
	}
}

func TestConfig_Normalize_NilReceiver(t *testing.T) {
	var c *Config
	if err := c.Normalize(); err != nil {
		t.Fatalf("Normalize() on nil receiver error = %v", err)
	}
}

func TestConfig_Allowed_Exact(t *testing.T) {
	c := &Config{Tools: []string{"github.get_file_contents"}}
	if !c.Allowed("github.get_file_contents") {
		t.Error("expected exact match to be allowed")
	}
	if c.Allowed("github.create_issue") {
		t.Error("expected a different tool to be denied")
	}
}

func TestConfig_Allowed_Glob(t *testing.T) {
	c := &Config{Tools: []string{"github.get_*"}}
	if !c.Allowed("github.get_file_contents") {
		t.Error("expected glob match to be allowed")
	}
	if !c.Allowed("github.get_issue") {
		t.Error("expected glob match to be allowed")
	}
	if c.Allowed("github.create_issue") {
		t.Error("expected non-matching tool to be denied")
	}
}

func TestConfig_Allowed_EmptyDenied(t *testing.T) {
	c := &Config{}
	if c.Allowed("github.get_file_contents") {
		t.Error("expected an empty allowlist to deny everything")
	}
	if c.Allowed("") {
		t.Error("expected an empty identity to be denied")
	}
}

func TestConfig_Allowed_NilConfig(t *testing.T) {
	var c *Config
	if c.Allowed("github.get_file_contents") {
		t.Error("expected a nil config to deny everything")
	}
}
