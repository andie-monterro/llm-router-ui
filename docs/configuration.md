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
| `--network` | (from config) | network shortcut; `agora` enables `agora.enabled` |
| `--agora-integration-file` | (from config/env) | path to an Agora integration file |

CLI flags override config file values. For example, `--address :9090` will override whatever `listen` is set to in the YAML.

## Config File Format

The config is loaded with `dd.MergeYAMLFile()`, which maps Go struct fields to `snake_case` YAML keys automatically. For example, the struct field `AllowExplicitModel` becomes the YAML key `allow_explicit_model`.

### Environment Variable Substitution

String values in the config support environment variable expansion using `${VAR}` syntax:

```yaml
providers:
  open_ai:
    api_key: "${OPENAI_API_KEY}"
  anthropic:
    api_key: "${ANTHROPIC_API_KEY}"
```

Variables are expanded at config load time using `os.ExpandEnv`.

## Top-Level Keys

```yaml
listen: ":8080"           # HTTP listen address (default: ":8080")

zrok:                     # optional: expose the gateway via zrok
  share:
    enabled: false
    mode: private         # public or private (default: private)
    token: ""             # existing persistent share token (private only)

agora:                    # optional: publish, serve, or connect over Agora
  enabled: false
  api_endpoint: ""
  env_root: ""
  advertisement:
    publish: true
    workgroup_ids: []
  serve:
    enabled: false

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
```

## Agora Configuration

Agora support is optional and additive. Enabling `agora:` does not disable the normal HTTP listener, zrok sharing, or direct provider URLs. The gateway can publish an Agora catalog advertisement, serve its API over an Agora Layer 1 tunnel, and connect to providers through Agora tunnels. See [docs/agora.md](agora.md) for the full reference.

```yaml
agora:
  enabled: true
  integration_file: ""              # optional partial agora config file
  api_endpoint: "http://127.0.0.1:8080"
  env_root: ""                      # optional Agora environment root
  instance_name: "engineering"      # default: llm-gateway
  description: "Engineering gateway"
  tunnel_mode: tcp                  # tcp, http, or udp
  advertisement:
    publish: true                   # default true
    workgroup_ids:
      - wg_abcdefghijkl
    contract_id: ""
    capabilities: []                # derived when empty
  serve:
    enabled: false
    backend_target: ""
    grants: []
```

`api_endpoint` is required when Agora is enabled and must match the enrolled Agora environment. Integration-file precedence is: `--agora-integration-file` flag, then `AGORA_LLM_GATEWAY_INTEGRATION_FILE`, then `agora.integration_file`.

## Provider Configuration

Each provider block is optional. Only configured providers are available for routing. A provider needs at minimum its required credentials (API key for OpenAI/Anthropic) to be initialized.

### OpenAI

```yaml
providers:
  open_ai:
    api_key: "${OPENAI_API_KEY}"      # required
    base_url: "https://api.openai.com" # optional: override for Azure or proxies
    zrok_share_token: ""               # optional: reach the API through a zrok share
    agora_tunnel: ""                  # optional: reach the API through an Agora tunnel
```

If `base_url` is omitted, it defaults to `https://api.openai.com`. Setting `base_url` lets you point at Azure OpenAI, a local proxy, or any OpenAI-compatible API.

### Anthropic

```yaml
providers:
  anthropic:
    api_key: "${ANTHROPIC_API_KEY}"      # required
    base_url: "https://api.anthropic.com" # optional: override base URL
    zrok_share_token: ""                  # optional: reach the API through a zrok share
    agora_tunnel: ""                     # optional: reach the API through an Agora tunnel
```

If `base_url` is omitted, it defaults to `https://api.anthropic.com`.

### Local Backend (Single Endpoint)

Configured under the `local` key. Works with Ollama, vLLM, llama-server, SGLang, or any OpenAI-compatible backend.

```yaml
providers:
  local:
    base_url: "http://localhost:11434"  # optional (default: http://localhost:11434)
    zrok_share_token: ""                # optional: reach the backend through a zrok share
    agora_tunnel: ""                   # optional: reach the backend through an Agora tunnel
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
      - name: agora-remote
        agora_tunnel: "gpu-tunnel"
    health_check:
      interval_seconds: 30   # default: 30
      timeout_seconds: 5     # default: 5
```

### Connecting Providers via Overlay Transports

Any provider can be reached through a zrok share or an Agora tunnel instead of a direct URL. Set `zrok_share_token` for zrok or `agora_tunnel` for Agora. Do not set both on the same provider or endpoint. See [docs/zrok.md](zrok.md) and [docs/agora.md](agora.md) for details.

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

1. Load and parse the YAML config file
2. Apply CLI flag overrides
3. Resolve Agora integration-file values, if Agora is enabled
4. Initialize Agora provider connects, if configured
5. Initialize providers (OpenAI, Anthropic, local/self-hosted) in order
6. Create the model-to-provider router
7. Initialize OpenTelemetry metrics (if enabled)
8. Initialize the semantic router (if configured)
9. Start the local HTTP server
10. Start the zrok share listener and Agora serve, if configured
11. Wait for SIGINT or SIGTERM, then shut down gracefully

On shutdown, the gateway closes all providers, deletes ephemeral zrok shares, releases zrok access objects, and removes Agora advertisement, serve, and connect resources.

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
      - name: agora-remote
        agora_tunnel: "gpu-tunnel"

metrics:
  enabled: true

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
