package migrate

import "testing"

func TestRegistrySettings_ValidateNil(t *testing.T) {
	var r *RegistrySettings
	if err := r.Validate(); err != nil {
		t.Errorf("nil RegistrySettings should be valid, got %v", err)
	}
}

func TestRegistrySettings_ValidateOK(t *testing.T) {
	r := &RegistrySettings{Sources: []RegistrySourceConfig{
		{Name: "acme", URL: "https://feed.acme.example/index.ndjson"},
		{Name: "internal", URL: "http://internal.example/index.ndjson"},
	}}
	if err := r.Validate(); err != nil {
		t.Errorf("expected valid, got %v", err)
	}
}

func TestRegistrySettings_ValidateRejectsMissingName(t *testing.T) {
	r := &RegistrySettings{Sources: []RegistrySourceConfig{{URL: "https://x.example/index.ndjson"}}}
	if err := r.Validate(); err == nil {
		t.Error("expected error for missing name")
	}
}

func TestRegistrySettings_ValidateRejectsDuplicateName(t *testing.T) {
	r := &RegistrySettings{Sources: []RegistrySourceConfig{
		{Name: "acme", URL: "https://a.example/index.ndjson"},
		{Name: "acme", URL: "https://b.example/index.ndjson"},
	}}
	if err := r.Validate(); err == nil {
		t.Error("expected error for duplicate name")
	}
}

func TestRegistrySettings_ValidateRejectsBadURL(t *testing.T) {
	r := &RegistrySettings{Sources: []RegistrySourceConfig{{Name: "acme", URL: "not-a-url"}}}
	if err := r.Validate(); err == nil {
		t.Error("expected error for non-http(s) url")
	}
}

func TestConfig_ValidateChecksRegistrySettings(t *testing.T) {
	cfg := &Config{Registry: &RegistrySettings{Sources: []RegistrySourceConfig{{URL: "https://x.example"}}}}
	if err := cfg.Validate(); err == nil {
		t.Error("Config.Validate should surface RegistrySettings errors")
	}
}
