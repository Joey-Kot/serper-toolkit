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

Use `uvx` to run the Python MCP server. `uvx` creates an isolated environment and installs package dependencies automatically:

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

## Go CLI

The Go CLI is located in the `cli` directory. It is a standalone command-line client named `serper`, separate from the Python MCP server.

The CLI reads the API key only from `SERPER_KEY`:

```bash
export SERPER_KEY="<your-serper-api-key>"
```

### Build From Source

Build for the current platform:

```bash
cd cli
go test ./...
go build -o serper .
```

Run it directly after building:

```bash
./serper --help
```

Build all release targets locally:

```bash
cd cli
mkdir -p dist

for target in windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  GOOS=${target%/*}
  GOARCH=${target#*/}
  binary=serper
  package="serper_${GOOS}_${GOARCH}.tar.gz"

  if [ "$GOOS" = windows ]; then
    binary=serper.exe
    package="serper_${GOOS}_${GOARCH}.zip"
  fi

  build_dir="build/${GOOS}_${GOARCH}"
  mkdir -p "$build_dir"
  CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH go build -trimpath -ldflags="-s -w" -o "$build_dir/$binary" .

  if [ "$GOOS" = windows ]; then
    (cd "$build_dir" && zip -q "../../dist/$package" "$binary")
  else
    tar -C "$build_dir" -czf "dist/$package" "$binary"
  fi

  (cd dist && sha256sum "$package" > "$package.sha256")
done

(cd dist && find . -maxdepth 1 -type f ! -name '*.sha256' ! -name 'SHA256SUMS' -printf '%f\n' | sort | xargs sha256sum > SHA256SUMS)
```

### CLI Commands

The CLI exposes the same 13 tool surfaces as subcommands:

```bash
serper aggregated --query "AI news" --search-num 20 --country US --language en --search-time day --timeout 120
serper general --query "AI news" --search-num 10 --country US --language en --search-time week
serper image --query "serper logo" --search-num 10
serper video --query "web scraping tutorial" --search-num 10
serper place --query "coffee" --location "Berlin,Germany"
serper maps --query "coffee" --ll "@52.5200,13.4050,14z" --search-num 20
serper reviews --cid "<google-cid>" --search-num 10 --sort-by newest
serper news --query "OpenAI" --search-time day
serper lens --image-url "https://example.com/image.jpg"
serper scholar --query "large language models"
serper shopping --query "mechanical keyboard"
serper patents --query "battery management"
serper scrape --output example --url "https://www.example.com"
```

Search-style commands output compact single-line JSON with the Python toolkit schema:

```json
{"success":true,"meta":{"requested_search_num":10,"effective_search_num":10,"pages_fetched":1,"result_count":1,"credits":1},"data":{"organic":[]},"credits":1}
```

### CLI Search Parameters

Shared search parameters:

- `--query` (required for `aggregated`, `general`, `image`, `video`, `place`, `maps`, `news`, `scholar`, `shopping`, `patents`): Search keywords.
- `--search-num` (optional): Number of results, range `1`-`100`. Defaults to `20` for `aggregated`, and `10` for other search subcommands.
- `--country` (optional): Country name or ISO code. Default is `US` where the Serper endpoint supports country targeting.
- `--language` (optional): Language code, such as `en`.
- `--search-time` (optional): One of `hour`, `day`, `week`, `month`, `year`. Supported by `aggregated`, `general`, `image`, `video`, and `news`.
- `--timeout` (optional): Request timeout in seconds. Must be `> 0`. Default is `120`.

Subcommand-specific parameters:

- `place`: `--location` is optional.
- `maps`: `--ll`, `--place-id`, and `--cid` are optional. When query-mode maps aggregation fetches more than one page, `--ll` is required.
- `reviews`: at least one of `--fid`, `--cid`, or `--place-id` is required. `--sort-by` is optional.
- `lens`: `--image-url` is required.

Normalization follows the Python toolkit:

- Non-image endpoints clamp `--search-num` to `1`-`100`, then round up to the nearest multiple of `10`.
- Image search uses `10` for requests `1`-`10`, and `100` for requests `11`-`100`.
- Search tools do not expose a `page` parameter; pagination is automatic.

### CLI Scrape Usage

Scrape a page and save the markdown export as `example.md` in the current directory:

```bash
serper scrape --output example --url "https://www.example.com" --timeout 120
```

Save the markdown export to a specific directory:

```bash
serper scrape --output example --path ./exports --url "https://www.example.com"
```

Scrape command parameters:

- `--output` (required): Export name. The CLI writes `<output>.md`.
- `--path` (optional): Directory where the markdown export is saved. Supports absolute and relative paths. Defaults to the current directory. If the directory does not exist, the CLI tries to create it before scraping.
- `--url` (required): Target URL to scrape.
- `--include-markdown` (optional): Request markdown content. Default is `true`.
- `--timeout` (optional): Request timeout in seconds. Must be `> 0`. Default is `120`.

Scrape output:

- On success, stdout is `true`, and the CLI writes `<output>.md`.
- On failure, stdout is `false` followed by the error reason, and no file is created or overwritten.

The generated markdown file uses this structure:

```markdown
## title:
## description:
## url:
## credits:

---

markdown content
```

## CI/CD

Two GitHub Actions workflows are included:

- `CI` (`.github/workflows/ci.yml`)
  - Trigger: push/PR on `main` and `dev`, plus manual `workflow_dispatch`
  - Python matrix: `3.12`, `3.13`
  - Steps: alias validation, unit tests, build, twine check
  - Go CLI: tests, six-platform builds, and upload to the `Latest` GitHub Release on push/manual runs

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

GNU General Public License v3.0 or later (GPL-3.0-or-later).
