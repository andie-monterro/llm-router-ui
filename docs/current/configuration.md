# Configuration

The gateway is configured with a YAML file and optional CLI flags. CLI flags take precedence over the config file.

## Running the Gateway

```bash
llm-gateway run <configPath>
```

| Argument/Flag | Default | Description |
|---|---|---|
| `<configPath>` | (required) | path to the config file |
| `--address` | (from config) | listen address, overrides `listen` in config |
| `--zrok` | `false` | enable zrok sharing, overrides `zrok.share.enabled` |
| `--zrok-mode` | (from config) | zrok share mode (`public` or `private`), overrides `zrok.share.mode` |

CLI flags override config file values. For example, `--address :9090` will override whatever `listen` is set to in the YAML.

## Config File Format

The config loader maps Go struct fields to `snake_case` YAML keys automatically. For example, the struct field `AllowExplicitModel` becomes the YAML key `allow_explicit_model`. The `api_keys` subtree has a stricter contract than the rest of the file: unknown fields and type coercions there are startup errors.

### Environment Variable Substitution

Credential and base-URL fields support environment variable expansion using `${VAR}` syntax:

```yaml
providers:
  open_ai:
    api_key: "${OPENAI_API_KEY}"
  anthropic:
    api_key: "${ANTHROPIC_API_KEY}"

api_keys:
  enabled: true
  sources:
    - type: http
      base_url: "${KEY_API_BASE_URL}"
      token: "${KEY_API_TOKEN}"
```

Variables are expanded once during config loading. This covers provider credentials and URLs, Agora's subsystem fields, inline virtual keys, and an HTTP key source's `base_url` and `token`. Key documents read from a file or HTTP response are data and are never expanded.

## Top-Level Keys

```yaml
listen: ":8080"           # HTTP listen address; opt-in/fallback (see note below)

zrok:                     # optional: expose the gateway via zrok
  share:
    enabled: false
    mode: private         # public or private (default: private)
    token: ""             # existing persistent share token (private only)

agora:                    # optional: serve/dial over the Agora overlay
  enabled: false
  serve:
    enabled: false        # serve the gateway over an operator-provisioned tunnel
    tunnel: ""            # bind-target tunnel name (default: instance_name)
  advertisement:
    publish: true         # tri-state; requires serve + workgroup_ids to publish
    workgroup_ids: []
  # see docs/agora.md for the full block (integration_file, api_endpoint, etc.)

providers:                # backend provider configs
  open_ai: ...
  anthropic: ...
  local: ...

metrics:                  # optional: OpenTelemetry metrics
  enabled: false

tracing:                  # optional: request body logging
  enabled: false
  max_content_length: 200 # max characters per message (default: 200)

routing:                  # optional: semantic routing
  ...                     # see docs/semantic-routing.md

api_keys:                 # optional: virtual-key authentication and reloadable sources
  enabled: false
  keys: []                # boot-resident inline keys
  sources: []             # file and HTTP key sources
  reload:
    max_staleness: 0      # unbounded by default
```

The gateway serves over every enabled transport at once. The local TCP `listen` is **opt-in or fallback**: it starts when `listen` is explicitly set, or as the fallback when no overlay serves (neither `zrok.share.enabled` nor `agora.serve.enabled`). Omit `listen` with an overlay serve enabled to stay private-only -- no plaintext local port is opened.

See [Virtual API Keys](api-keys.md) for authentication and restrictions, and [Key Sources](key-sources.md) for the complete source, record, refresh, and staleness contract.

## Provider Configuration

Each provider block is optional. Only configured providers are available for routing. A provider needs at minimum its required credentials (API key for OpenAI/Anthropic) to be initialized.

### OpenAI

```yaml
providers:
  open_ai:
    api_key: "${OPENAI_API_KEY}"      # required
    base_url: "https://api.openai.com" # optional: override for Azure or proxies
    zrok_share_token: ""               # optional: reach the API through a zrok share
    agora_tunnel: ""                   # optional: reach the API through an Agora tunnel (wins over zrok)
```

If `base_url` is omitted, it defaults to `https://api.openai.com`. Setting `base_url` lets you point at Azure OpenAI, a local proxy, or any OpenAI-compatible API.

### Anthropic

```yaml
providers:
  anthropic:
    api_key: "${ANTHROPIC_API_KEY}"      # required
    base_url: "https://api.anthropic.com" # optional: override base URL
    zrok_share_token: ""                  # optional: reach the API through a zrok share
    agora_tunnel: ""                      # optional: reach the API through an Agora tunnel (wins over zrok)
```

If `base_url` is omitted, it defaults to `https://api.anthropic.com`.

### Local Backend (Single Endpoint)

Configured under the `local` key. Works with Ollama, vLLM, llama-server, SGLang, or any OpenAI-compatible backend.

```yaml
providers:
  local:
    base_url: "http://localhost:11434"  # optional (default: http://localhost:11434)
    zrok_share_token: ""                # optional: reach the backend through a zrok share
    agora_tunnel: ""                    # optional: reach the backend through an Agora tunnel (wins over zrok)
```

### Local Backend (Multi-Endpoint)

When `endpoints` is present, it replaces `base_url` and `zrok_share_token`. See [docs/multi-endpoint.md](multi-endpoint.md) for details.

```yaml
providers:
  local:
    endpoints:
      - name: gpu-box-1
        base_url: "http://10.0.0.1:11434"
        weight: 3             # optional: receives ~3x traffic (default: 1)
      - name: gpu-box-2
        base_url: "http://10.0.0.2:11434"
      - name: remote
        zrok_share_token: "abc123"
    health_check:
      interval_seconds: 30   # default: 30
      timeout_seconds: 5     # default: 5
```

### Connecting Providers via Zrok

Any provider can be reached through a zrok share instead of (or alongside) a direct URL. Set `zrok_share_token` on the provider config. The gateway creates a zrok access object that provides an HTTP client routing through the zrok overlay network. See [docs/zrok.md](zrok.md) for details.

### Connecting Providers via Agora

Any provider can also be reached over an Agora tunnel by setting `agora_tunnel`. This requires `agora.enabled: true`. The gateway attaches each unique tunnel once at startup and hands the provider a shared HTTP client that dials the tunnel. The `base_url` passes through unchanged, so a cloud-egress provider keeps its real `https://` URL and TLS rides the tunnel end-to-end. When both `agora_tunnel` and `zrok_share_token` are set on the same provider, **Agora wins**. See [docs/agora.md](agora.md) for the full block, serving, and the three scenarios.

## Metrics Configuration

```yaml
metrics:
  enabled: true       # enable OpenTelemetry metrics with Prometheus exporter
```

When enabled, the Prometheus metrics endpoint is served at `GET /metrics` on the main listener. See [docs/metrics.md](metrics.md) for the full list of instruments.

## Tracing Configuration

```yaml
tracing:
  enabled: true             # enable request body logging
  max_content_length: 200   # max characters per message in log output (default: 200)
```

When enabled, each chat completion request is logged with a structured summary showing the requested model, message count, streaming flag, tool count, and each message's role and truncated content. Newlines in message content are escaped to keep each log entry on a single line.

This is useful for debugging semantic routing decisions -- it shows exactly what the client sent, making it easy to identify why a heuristic rule matched or why a request was routed unexpectedly.

## Startup Sequence

1. Load the YAML file; strictly bind and validate `api_keys`; resolve environment references and normalize defaults.
2. Apply CLI flag overrides.
3. Initialize Agora, attach every provider and key-source dial tunnel, and prepare serving when enabled.
4. Initialize providers and the model-to-provider router.
5. Initialize OpenTelemetry metrics when enabled.
6. Boot-load inline, file, and HTTP key contributions; publish one composed snapshot; start source refresh loops.
7. Initialize the semantic router when configured.
8. Start the HTTP server over every enabled transport (local, zrok share, and/or Agora tunnel).
9. Dispatch `SIGHUP` to reloadable key sources on Unix; wait for SIGINT or SIGTERM to shut down.

On shutdown, HTTP servers drain first. The key store then cancels and joins every refresh before the gateway closes providers, its zrok share, zrok access objects, and the Agora subsystem. That ordering ensures a key-source request finishes using its borrowed overlay transport before the transport owner is closed. Ephemeral zrok shares are deleted; supplied persistent shares are only disconnected.

## Full Example

```yaml
listen: ":8080"

providers:
  open_ai:
    api_key: "${OPENAI_API_KEY}"

  anthropic:
    api_key: "${ANTHROPIC_API_KEY}"

  local:
    endpoints:
      - name: local
        base_url: "http://localhost:11434"
      - name: remote
        zrok_share_token: "abc123"

metrics:
  enabled: true

api_keys:
  enabled: true
  keys:
    - name: breakglass
      key: "${BREAKGLASS_KEY}"
  sources:
    - type: file
      name: managed
      path: "/etc/llm-gateway/keys.yaml"
      watch: true
      poll_interval: "30s"
  reload:
    max_staleness: "10m"

tracing:
  enabled: true
  max_content_length: 300

routing:
  default_route: general

  heuristics:
    enabled: true
    rules:
      - match:
          keywords: ["translate"]
        route: general
      - match:
          keywords: ["code", "debug", "refactor"]
          exclude: ["code fences", "code block"]
        route: coding

  semantic:
    enabled: true
    provider: local
    model: nomic-embed-text
    threshold: 0.75
    ambiguous_threshold: 0.5

  routes:
    - name: coding
      model: claude-haiku-4-5-20251001
      description: "code generation and debugging"
      examples:
        - "write a python function to sort a list"
    - name: general
      model: qwen3-vl:30b
      description: "general knowledge and conversation"
      examples:
        - "what is the capital of France"
```
