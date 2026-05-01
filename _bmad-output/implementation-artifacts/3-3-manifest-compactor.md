# Story 3-3: Manifest Compactor (LLM Distillation)

## Header

| Field | Value |
|-------|-------|
| ID | 3-3 |
| Key | manifest-compactor |
| Epic | Epic 3: Context Optimization (JIT Discovery & Compactor) |
| Title | Implement Manifest Compactor (LLM Distillation) |
| Status | backlog |
| Estimated Points | 8 |

## User Story

**As a** developer,
**I want to** compact raw MCP manifests into token-dense signatures using LLM distillation,
**So that** even the full schemas are optimized for token efficiency.

## Acceptance Criteria (BDD Format)

### AC1: LLM Distillation Pipeline

**Given** a raw MCP manifest with verbose descriptions
**When** the Compactor processes it
**Then** it sends the manifest to a configured cheap LLM (e.g., GPT-4o-mini)
**And** receives a distilled version with shorter descriptions
**And** preserves all parameter names and types exactly

### AC2: Distilled Schema Usage

**Given** a distilled manifest signature
**When** the IDE requests tool details
**Then** the distilled schema is used instead of the original
**And** the token count is reduced by 50-80% while preserving functionality

### AC3: Graceful Degradation

**Given** LLM distillation is configured but the LLM is unavailable
**When** a manifest needs compaction
**Then** the proxy falls back to the original manifest
**And** logs a warning to stderr
**And** continues operating without compaction

## Developer Context

### Technical Requirements

1. **LLM Client Interface**
   - Create `LLMClient` interface in `pkg/compactor/` to support multiple providers
   - Implement OpenAI-compatible client for GPT-4o-mini
   - Support configurable endpoint, API key, model name
   - Implement retry with exponential backoff (3 attempts)

2. **Distillation Prompt Design**
   - System prompt instructs LLM to preserve all technical details
   - User prompt contains the raw manifest JSON
   - Response format: JSON matching original structure with condensed descriptions
   - Max tokens: 2000 (response)

3. **Distillation Request/Response**
   - Input: Raw tool manifest (name, description, parameters)
   - Output: Same structure with description <= 50 chars, parameter names unchanged
   - Preserve: tool name, parameter names, parameter types, required flags

4. **Caching Distilled Manifests**
   - Store distilled manifests alongside original in registry
   - Cache file: `~/.config/leanproxy/distilled/{server-name}.json`
   - Invalidate on server manifest refresh

5. **Configuration**
   - Add `compactor.llm-provider` config option (default: "openai")
   - Add `compactor.llm-endpoint` config option
   - Add `compactor.llm-api-key` config option (from env: `LEANPROXY_LLM_API_KEY`)
   - Add `compactor.llm-model` config option (default: "gpt-4o-mini")
   - Add `compactor.enabled` config option (default: true)

### Architecture Compliance

- **Naming**: `camelCase` for Go functions/variables, `kebab-case` for CLI flags
- **Error Handling**: `fmt.Errorf("context: %w", err)` for error wrapping
- **Logging**: `log/slog` for structured logging to stderr
- **Project Structure**: `pkg/compactor/` for distillation logic

### File Structure

```
pkg/
├── compactor/
│   ├── compactor.go         # Main compactor orchestration
│   ├── llm_client.go        # LLM client interface and implementation
│   ├── llm_client_test.go   # Unit tests for LLM client
│   ├── prompt.go            # Prompt templates
│   ├── manifest.go          # Manifest processing
│   └── cache.go             # Distilled manifest caching
└── registry/
    └── registry.go         # Updated to support distilled schemas
```

### Testing Requirements

1. **Unit Tests**
   - Test LLM client request/response parsing
   - Test prompt generation
   - Test manifest transformation logic
   - Test cache read/write

2. **Integration Tests** (requires mock LLM or recorded responses)
   - Test full distillation pipeline
   - Test token reduction percentage calculation
   - Test fallback behavior

3. **Performance Tests**
   - Verify distillation completes within 5 seconds
   - Verify subsequent cached distillations <10ms

## Implementation Notes

### LLM Client Interface

```go
// pkg/compactor/llm_client.go
type LLMClient interface {
    Distill(ctx context.Context, manifest RawManifest) (*DistilledManifest, error)
}

type OpenAIClient struct {
    endpoint string
    apiKey   string
    model    string
    httpClient *http.Client
}
```

### Distillation Result

```go
type DistilledManifest struct {
    ServerName string
    Tools      []DistilledTool
    OriginalHash string    // SHA256 of original for cache invalidation
    DistilledAt time.Time
}

type DistilledTool struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`  // Max 50 chars
    Parameters  json.RawMessage `json:"parameters"`   // Unchanged
}
```

### Distillation Prompt

```
System: You are a token optimization assistant. Reduce tool descriptions to 
minimum necessary characters while preserving all technical accuracy. 
Output valid JSON only. Preserve parameter names, types, and required flags exactly.

User: Optimize this MCP tool manifest for token efficiency:
{manifest_json}
```

### Configuration Schema

```yaml
compactor:
  enabled: true
  llm-provider: "openai"
  llm-endpoint: "https://api.openai.com/v1/chat/completions"
  llm-api-key: "${LEANPROXY_LLM_API_KEY}"
  llm-model: "gpt-4o-mini"
  cache-dir: "~/.config/leanproxy/distilled"
```
