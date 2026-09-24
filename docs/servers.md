# First-party MCP servers

The repository ships four small stdio MCP servers under `servers/`:

| Server | Source | Backend | Configured by |
|--------|--------|---------|---------------|
| Postgres | `servers/postgres` | A PostgreSQL database | `LEANPROXY_POSTGRES_*` |
| Redis | `servers/redis` | A Redis server | `LEANPROXY_REDIS_*` |
| Filesystem | `servers/filesystem` | One local directory | `LEANPROXY_FILESYSTEM_ROOTS` |
| GitHub | `servers/github` | The GitHub REST API | `GITHUB_TOKEN` |

They are ordinary MCP servers. LeanProxy-MCP does not start them on its own.
You declare them in `leanproxy_servers.yaml` like any other stdio server, and
they go through the same pipeline (redaction, policy, pinning, governor and so
on).

They are deliberately small. Official vendor servers exist for most of these
backends and usually cover more. Use these when you want a minimal, auditable
surface.

!!! note "Not included in release archives"
    The release archives contain only the `leanproxy-mcp` binary. Build the
    servers from a clone of the repository (Go 1.25 or later):

    ```bash
    git clone https://github.com/mmornati/leanproxy-mcp.git
    cd leanproxy-mcp
    go build -o ~/bin/leanproxy-mcp-postgres   ./servers/postgres
    go build -o ~/bin/leanproxy-mcp-redis      ./servers/redis
    go build -o ~/bin/leanproxy-mcp-filesystem ./servers/filesystem
    go build -o ~/bin/leanproxy-mcp-github     ./servers/github
    ```

    Use the absolute path of the built binary as `stdio.command`.

## Common behaviour

- Transport: newline-delimited JSON-RPC on stdin/stdout. Logs go to stderr.
- Methods: `initialize`, `tools/list`, `tools/call`, `ping`. Any other method
  returns "method not found". The servers report MCP protocol version
  `2024-11-05` and only the `tools` capability (no resources or prompts).
- A tool result is returned as a single `text` content block that holds a JSON
  document.
- Configuration comes only from environment variables. Since v0.11, stdio
  children get a least-privilege environment, so you must pass these variables
  explicitly with `stdio.env` or `stdio.env_passthrough`. See
  [Child Process Environment](configuration.md#child-process-environment-env-env_passthrough-inherit_env).

## Postgres

Query a PostgreSQL database. Read-only by default.

**Environment variables.** The full table, with the reasoning behind the
read-only design, is in
[Configuration › First-Party Servers](configuration.md#first-party-servers-postgres-and-redis).

| Variable | Default |
|----------|---------|
| `LEANPROXY_POSTGRES_CONNECTION` | required, e.g. `postgres://user:pass@host:5432/db` |
| `LEANPROXY_POSTGRES_POOL_SIZE` | `10` |
| `LEANPROXY_POSTGRES_STATEMENT_TIMEOUT` | `30s` |
| `LEANPROXY_POSTGRES_READ_ONLY` | `true` |

**Tools.**

| Tool | Arguments | Notes |
|------|-----------|-------|
| `postgresql_query` | `query` | Runs inside a `READ ONLY` transaction that is rolled back, whatever `READ_ONLY` says. Only `SELECT`, `EXPLAIN` and `WITH` are accepted. One statement per call. |
| `postgresql_list_tables` | `schema` (optional, default `public`) | Tables with estimated row counts. |
| `postgresql_describe` | `table` (e.g. `public.users`) | Column names, types, nullability. |
| `postgresql_execute` | `statement` | INSERT, UPDATE, DELETE, DDL. **Only registered when `LEANPROXY_POSTGRES_READ_ONLY=false`.** |

**Safety defaults.**

- Read-only mode is on unless you set `LEANPROXY_POSTGRES_READ_ONLY=false`.
- The server connects and pings the database at start. It exits with an error
  if the connection string is missing or the database is unreachable.
- The flag is defense in depth. Connect with a database role that has only the
  privileges you want to grant.

```yaml
servers:
  - name: postgres
    transport: stdio
    stdio:
      command: /home/me/bin/leanproxy-mcp-postgres
      env:
        - "LEANPROXY_POSTGRES_CONNECTION=${APP_DB_URL}"   # expanded from the proxy's environment
        - "LEANPROXY_POSTGRES_STATEMENT_TIMEOUT=15s"
```

## Redis

Read and write keys on a Redis server.

**Environment variables.** Full table in
[Configuration › Redis](configuration.md#redis-serversredis).

| Variable | Default |
|----------|---------|
| `LEANPROXY_REDIS_ADDRESS` | `127.0.0.1:6379` |
| `LEANPROXY_REDIS_PASSWORD` | none |
| `LEANPROXY_REDIS_POOL_SIZE` | `10` |
| `LEANPROXY_REDIS_TLS` | off (`true` or `1` enables TLS 1.2+) |
| `LEANPROXY_REDIS_DIAL_TIMEOUT` | `5s` |
| `LEANPROXY_REDIS_COMMAND_TIMEOUT` | `5s` |
| `LEANPROXY_REDIS_MAX_BULK_LEN` | `16777216` (16 MiB) |
| `LEANPROXY_REDIS_MAX_ARRAY_LEN` | `1000000` |

The server always uses database 0. There is no variable to select another one.

**Tools.**

| Tool | Arguments | Redis command |
|------|-----------|---------------|
| `redis_get` | `key` | `GET` |
| `redis_set` | `key`, `value`, `ttl_seconds` (optional) | `SET` (with `EX` when a TTL is given) |
| `redis_delete` | `keys` (array) | `DEL` |
| `redis_keys` | `pattern` (glob, e.g. `user:*`) | `KEYS` |
| `redis_exists` | `keys` (array) | `EXISTS` |

**Safety defaults.**

- There is no generic "run any command" tool. `FLUSHALL`, `CONFIG`, `EVAL` and
  similar commands cannot be reached.
- There is **no read-only mode**: `redis_set` and `redis_delete` are always
  exposed. To make it read-only, deny both tools with a
  [policy](configuration.md#per-tool-policy-policy) rule (`match: "redis.redis_set"`
  and `match: "redis.redis_delete"`, `action: deny`), or remove write
  permissions from the Redis `default` user. The server authenticates with
  `AUTH <password>`, so it always connects as `default`.
- `redis_keys` uses `KEYS`, which blocks Redis while it scans. Avoid broad
  patterns on large production databases.
- The server opens all pool connections at start and exits if Redis is
  unreachable.

```yaml
servers:
  - name: redis
    transport: stdio
    stdio:
      command: /home/me/bin/leanproxy-mcp-redis
      env:
        - "LEANPROXY_REDIS_ADDRESS=cache.internal:6380"
        - "LEANPROXY_REDIS_TLS=true"
      env_passthrough: ["LEANPROXY_REDIS_PASSWORD"]   # copied from the proxy's environment
```

## Filesystem

Read and write files under one local directory.

**Environment variables.**

| Variable | Default | Description |
|----------|---------|-------------|
| `LEANPROXY_FILESYSTEM_ROOTS` | required | Comma-separated list of directories. **Only the first entry is used** (see the warning below). |

The server exits at start if the variable is empty or the first directory
cannot be opened. Use an absolute path: a relative path is resolved against the
server's working directory (`stdio.cwd`).

!!! warning "Only the first root is served"
    The server accepts a comma-separated list and logs all of it, but every
    tool resolves paths against the **first** directory only. Paths under the
    other directories cannot be reached. Run one server entry per directory if
    you need several.

**Tools.** All paths are relative to the root. Absolute paths and paths that
contain `..` are rejected. The root is opened with Go's `os.Root`, so symbolic
links cannot lead outside it either.

| Tool | Arguments | Notes |
|------|-----------|-------|
| `read_file` | `path` | Returns at most the first 1 MiB. Larger files come back with `"truncated": true`. |
| `read_multiple_files` | `paths` (array) | Same 1 MiB limit per file. Per-file errors are listed in `errors`; the call itself succeeds. |
| `write_file` | `path`, `content` | Creates or overwrites the file. Creates missing parent directories. |
| `list_directory` | `path` (`.` for the root) | Name, size, `is_dir` and mode of each entry. |
| `file_info` | `path` | Name, size, `is_dir`, mode, modification time (UTC). |
| `search_files` | `pattern`, `root` (optional subdirectory, default `.`) | Matches each file's path relative to the root with Go's `filepath.Match`. |

!!! note "`search_files` patterns"
    `filepath.Match` has no recursive `**`, and `*` does not cross `/`. The
    pattern is matched against the whole relative path, so `*.go` finds only
    Go files at the top level, and `**/*.go` behaves like `*/*.go` (exactly
    one directory deep). Write one pattern per depth, for example `*.go`,
    `*/*.go`, `*/*/*.go`.

**Safety defaults.**

- Access is confined to one directory.
- There is **no read-only mode**: `write_file` is always exposed. To block
  writes, deny `write_file` with a [policy](configuration.md#per-tool-policy-policy)
  rule (`match: "files.write_file"`, `action: deny`), or make the directory
  read-only for the user that runs the server.

```yaml
servers:
  - name: files
    transport: stdio
    stdio:
      command: /home/me/bin/leanproxy-mcp-filesystem
      env:
        - "LEANPROXY_FILESYSTEM_ROOTS=/home/me/projects/app"
```

## GitHub

Read repositories and issues from GitHub, and open pull requests when a token
is set.

**Environment variables.**

| Variable | Default | Description |
|----------|---------|-------------|
| `GITHUB_TOKEN` | none | Personal access token. Without it, the server runs unauthenticated and read-only. |

**Tools.**

| Tool | Arguments | Available |
|------|-----------|-----------|
| `list_repos` | `owner`, `type` (optional), `per_page` (optional, 1–100, default 30) | Always |
| `get_issue` | `owner`, `repo`, `issue_number` | Always |
| `create_pr` | `owner`, `repo`, `title`, `head`, `base`, `body` (optional) | Only with `GITHUB_TOKEN` |

`list_repos` calls GitHub's "list repositories for a user" endpoint
(`GET /users/{owner}/repos`). That endpoint returns public repositories only,
with or without a token. Only the first page is returned.

**Safety defaults.**

- Without `GITHUB_TOKEN`, `create_pr` is not listed and calling it fails. The
  server prints a notice on stderr at start.
- With a token, what the server can do is limited by the token's scopes. Use a
  fine-grained token limited to the repositories you need.
- The server keeps its own client-side budget of 5000 calls per hour. When it
  is used up, calls fail with a "rate limit exceeded" error until it resets.
  GitHub's own limit for unauthenticated requests is much lower (60 per hour),
  so without a token you will usually hit GitHub's limit first.

```yaml
servers:
  - name: github
    transport: stdio
    stdio:
      command: /home/me/bin/leanproxy-mcp-github
      env_passthrough: ["GITHUB_TOKEN"]   # omit to run read-only
```

## See also

- [Configuration › First-Party Servers](configuration.md#first-party-servers-postgres-and-redis)
  for the detailed Postgres and Redis reference.
- [Security](security.md) for the protections LeanProxy-MCP applies to every
  server, first-party or not.
