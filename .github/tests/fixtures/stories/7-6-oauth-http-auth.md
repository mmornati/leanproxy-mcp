---
id: 7-6
key: oauth-http-auth
epic: epic-7
title: OAuth2 Authentication Support for HTTP Transport
---

# Story 7-6: OAuth2 Authentication Support for HTTP Transport

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 7-6 |
| **Key** | oauth-http-auth |
| **Epic** | epic-7 |
| **Title** | OAuth2 Authentication Support for HTTP Transport |

## Story Requirements

### User Story

As a developer,
I want to connect to OAuth2-protected MCP servers via HTTP transport,
So that LeanProxy-MCP can authenticate to enterprise MCP servers requiring authentication.

### Acceptance Criteria

1. The system can configure OAuth2 authentication for HTTP transport servers in `leanproxy_servers.yaml`
2. The HTTP client pool properly sends authentication headers when connecting to OAuth2 servers
3. Token refresh is handled automatically by the mcp-go library
4. Configuration supports: client_id, client_secret, scopes, auth_type (bearer, oauth2)
5. Documentation is updated with authentication configuration examples

## Developer Context

### Technical Requirements

- Add OAuth2 configuration to ServerConfig in `pkg/migrate/config.go`
- Update HTTPClientPool in `pkg/pool/http_pool.go` to use OAuth when configured
- Use `mcp-go` library's built-in OAuth support: `WithHTTPOAuth()` option
- Support auth types: bearer (simple API key), oauth2 (full OAuth2 flow)

### Configuration Schema

```yaml
servers:
  - name: my-oauth-server
    transport: http
    http:
      url: https://api.example.com/mcp
      auth:
        type: oauth2           # bearer | oauth2
        client_id: "my-client"
        client_secret: "secret"
        scopes:
          - mcp:read
          - mcp:write
```

### Files to Modify

1. `pkg/migrate/config.go` - Add AuthConfig struct
2. `pkg/pool/http_pool.go` - Add OAuth support in HTTPClientServer
3. `docs/configuration.md` - Add authentication documentation

## Implementation Checklist

- [ ] Add AuthConfig struct to pkg/migrate/config.go
- [ ] Update HTTPClientServer to support OAuth config
- [ ] Add WithHTTPOAuth option when creating mcp-go client
- [ ] Update docs/configuration.md with auth examples
- [ ] Add unit tests for OAuth config parsing
- [ ] Verify tests pass
- [ ] Run lint and typecheck