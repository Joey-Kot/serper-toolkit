# Serper MCP Toolkit

A rebuilt Serper MCP server based on the `firecrawl-toolkit` architecture.

## Highlights

- Unified async request layer (`httpx` + connection pooling + retries)
- Compact JSON responses (single-line) for lower token usage
- Stable mapped response schemas (no upstream raw passthrough)
- Multi-page aggregation for non-image search endpoints (10 results/page granularity)
- Image search `search_num` normalization to `10` or `100`
- Multi-transport startup (`STDIO` / `HTTP` / `SSE`, mutually exclusive)
- Process lock to prevent duplicate server instances

## Tools

- `serper-aggregated-search`
- `serper-general-search`
- `serper-image-search`
- `serper-video-search`
- `serper-place-search`
- `serper-maps-search`
- `serper-reviews-search`
- `serper-news-search`
- `serper-lens-search`
- `serper-scholar-search`
- `serper-shopping-search`
- `serper-patents-search`
- `serper-scrape`

## Core Parameter Rules

- Search tools do **not** expose `page`; internal pagination is automatic.
- Non-image endpoints:
  - `search_num` is clamped to `1..100`
  - then rounded **up** to nearest multiple of 10 (`25 -> 30`)
- Image endpoint:
  - `1..10 -> 10`, `11..100 -> 100`
- Maps endpoint:
  - when aggregation requires page > 1 and query mode is used, `ll` is required.

## Environment Variables

### API and network

- `SERPER_API_KEY`
- `SERPER_HTTP2` (0/1)
- `SERPER_MAX_CONNECTIONS`
- `SERPER_KEEPALIVE`
- `SERPER_MAX_CONCURRENT_REQUESTS`
- `SERPER_MAX_WORKERS`
- `SERPER_RETRY_COUNT`
- `SERPER_RETRY_BASE_DELAY`
- `SERPER_ENDPOINT_CONCURRENCY` (JSON, e.g. `{"search":10,"scrape":2}`)
- `SERPER_ENDPOINT_RETRYABLE` (JSON, e.g. `{"scrape":false}`)

### Transport selection (exactly one must be enabled)

- `SERPER_MCP_ENABLE_STDIO`
- `SERPER_MCP_ENABLE_HTTP`
- `SERPER_MCP_ENABLE_SSE`

### Transport host/port

- `SERPER_MCP_HTTP_HOST`
- `SERPER_MCP_HTTP_PORT`
- `SERPER_MCP_SSE_HOST`
- `SERPER_MCP_SSE_PORT`
- `SERPER_MCP_HOST` (fallback)
- `SERPER_MCP_PORT` (fallback)

### Lock file

- `SERPER_MCP_LOCK_FILE` (default `/tmp/serper_mcp.lock` on Unix)

## MCP Client Example

```json
{
  "mcpServers": {
    "serper-toolkit": {
      "command": "uvx",
      "args": ["serper-toolkit"],
      "env": {
        "SERPER_API_KEY": "<your-serper-api-key>",
        "SERPER_MCP_ENABLE_STDIO": "1"
      }
    }
  }
}
```

## CI/CD

Two GitHub Actions workflows are included:

- `CI` (`.github/workflows/ci.yml`)
  - Trigger: push/PR on `main`
  - Python matrix: `3.12`, `3.13`
  - Steps: alias validation, unit tests, build, twine check

- `Publish to PyPI` (`.github/workflows/publish-pypi.yml`)
  - Trigger: push on `main` (automatic), plus manual `workflow_dispatch`
  - Preflight: alias validation, tests, build, twine check, version-not-on-PyPI check
  - Publish: trusted publishing via OIDC to PyPI

### Required repository settings

1. Create GitHub Environment: `pypi`
2. Configure PyPI Trusted Publisher for this repo/workflow/branch:
   - Workflow file: `.github/workflows/publish-pypi.yml`
   - Branch: `main`
3. Ensure Actions permissions allow OIDC (`id-token: write` in workflow is already set)

## Development Validation

```bash
python3 scripts/validate_country_aliases.py
python3 -m unittest discover -s tests -p "test_*.py"
python3 -m build
python3 -m twine check dist/*
```

## License

MIT
