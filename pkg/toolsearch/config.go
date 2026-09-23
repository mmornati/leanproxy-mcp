package toolsearch

import (
	"fmt"

	"github.com/mmornati/leanproxy-mcp/pkg/cache/embedder"
)

// Config is the `tool_search:` block of the proxy config. A nil *Config
// means BM25 with the default synonyms and no hybrid mode.
type Config struct {
	// Synonyms adds query expansions ("k8s": "kubernetes cluster") to the
	// defaults (DefaultSynonyms); an entry with the same key replaces the
	// default one. Each key must be a single word.
	Synonyms map[string]string `yaml:"synonyms,omitempty"`
	// DisableDefaultSynonyms drops the built-in synonym table.
	DisableDefaultSynonyms bool `yaml:"disable_default_synonyms,omitempty"`
	// Hybrid turns on BM25 + embedding ranking. Off by default.
	Hybrid *HybridConfig `yaml:"hybrid,omitempty"`
}

// HybridConfig is `tool_search.hybrid`.
type HybridConfig struct {
	Enabled bool `yaml:"enabled"`
	// Embedder selects the embedding provider (ollama or openai), with
	// the same keys as elsewhere in the config.
	Embedder embedder.Config `yaml:"embedder"`
}

// HybridEnabled reports whether hybrid mode is configured on.
func (c *Config) HybridEnabled() bool {
	return c != nil && c.Hybrid != nil && c.Hybrid.Enabled
}

// Validate rejects synonym entries that cannot be used and an enabled
// hybrid block without a valid embedder. A nil receiver is valid.
func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	for k, v := range c.Synonyms {
		if n := len(Tokenize(k)); n != 1 {
			return fmt.Errorf("tool_search.synonyms: key %q must be one word that is not a stopword", k)
		}
		if len(Tokenize(v)) == 0 {
			return fmt.Errorf("tool_search.synonyms: %q expands to no searchable word", k)
		}
	}
	if c.HybridEnabled() {
		if err := c.Hybrid.Embedder.Validate(); err != nil {
			return fmt.Errorf("tool_search.hybrid: %w", err)
		}
	}
	return nil
}

// Options returns the index options for this config (the synonym table).
func (c *Config) Options() Options {
	syn := DefaultSynonyms()
	if c == nil {
		return Options{Synonyms: syn}
	}
	if c.DisableDefaultSynonyms {
		syn = map[string]string{}
	}
	for k, v := range c.Synonyms {
		syn[k] = v
	}
	return Options{Synonyms: syn}
}
