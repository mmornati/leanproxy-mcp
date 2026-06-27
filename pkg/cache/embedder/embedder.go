package embedder

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type Provider string

const (
	ProviderOllama Provider = "ollama"
	ProviderOpenAI Provider = "openai"
)

type EmbedRequest struct {
	ToolName string
	Args     json.RawMessage
}

func (r EmbedRequest) Input() string {
	var b strings.Builder
	b.WriteString(r.ToolName)
	b.WriteByte(':')
	if len(r.Args) > 0 {
		keys := make([]string, 0, 8)
		var parsed map[string]interface{}
		if err := json.Unmarshal(r.Args, &parsed); err == nil {
			for k := range parsed {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				b.WriteByte(' ')
				b.WriteString(k)
				b.WriteByte('=')
				val, _ := json.Marshal(parsed[k])
				b.Write(val)
			}
		} else {
			b.Write(r.Args)
		}
	}
	return b.String()
}

type Embedding struct {
	Vector []float32
	Model  string
}

type Embedder interface {
	Embed(ctx context.Context, req EmbedRequest) (Embedding, error)
	Provider() Provider
	Close() error
}

type Config struct {
	Provider Provider      `yaml:"provider"`
	Ollama   *OllamaConfig `yaml:"ollama,omitempty"`
	OpenAI   *OpenAIConfig `yaml:"openai,omitempty"`
}

func (c Config) Validate() error {
	switch c.Provider {
	case ProviderOllama:
		if c.Ollama == nil {
			return fmt.Errorf("embedder: ollama config required when provider=%q", c.Provider)
		}
		return c.Ollama.Validate()
	case ProviderOpenAI:
		if c.OpenAI == nil {
			return fmt.Errorf("embedder: openai config required when provider=%q", c.Provider)
		}
		return c.OpenAI.Validate()
	default:
		return fmt.Errorf("embedder: unknown provider %q", c.Provider)
	}
}
