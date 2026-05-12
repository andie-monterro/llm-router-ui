# Agora Integration

The gateway can join an [Agora](https://github.com/openziti/agora)
network as a first-class node. Agora support is additive: enabling it
does not disable the normal HTTP listener, zrok sharing, direct
provider URLs, or zrok-backed provider access.

Agora is used in three independent ways:

1. Publish a catalog advertisement so other Agora agents can discover
   the gateway.
2. Serve the gateway API over an Agora Layer 1 tunnel.
3. Connect to upstream providers through Agora Layer 1 tunnels.

## Prerequisites

Agora mode requires:

- An enrolled Agora environment on the gateway host.
- A reachable Agora controller.
- An `agora.api_endpoint` value matching the enrolled environment.

`agora.api_endpoint` is validate-only. The gateway compares it to the
endpoint in the enrolled Agora environment and exits if they differ;
it does not rewrite Agora environment files.

## Configuration

```yaml
listen: ":8080"

agora:
  enabled: true
  integration_file: ""          # optional; see "Integration File"

  api_endpoint: "http://127.0.0.1:8080"
  env_root: ""                  # optional; SDK default/AGORA_ENV_ROOT may apply

  instance_name: "engineering"  # default: llm-gateway
  description: "Engineering LLM gateway"
  tunnel_mode: tcp              # tcp, http, or udp; default: tcp

  advertisement:
    publish: true               # default true when agora.enabled is true
    workgroup_ids:
      - wg_abcdefghijkl         # required when publish is true
    contract_id: ""             # optional con_... ID
    capabilities: []            # derived when empty

  serve:
    enabled: false
    backend_target: ""          # defaults to the active local listener
    grants: []                  # additive grants handled by Agora
```

Provider blocks can opt into Agora per provider:

```yaml
providers:
  open_ai:
    api_key: "${OPENAI_API_KEY}"
    agora_tunnel: "openai-relay"

  anthropic:
    api_key: "${ANTHROPIC_API_KEY}"
    agora_tunnel: "anthropic-relay"

  local:
    agora_tunnel: "local-llm"
```

For local multi-endpoint mode, each endpoint chooses its own
transport:

```yaml
providers:
  local:
    endpoints:
      - name: local-gpu
        base_url: "http://localhost:11434"
      - name: remote-gpu
        agora_tunnel: "remote-gpu-tunnel"
      - name: zrok-gpu
        zrok_share_token: "abc123"
```

Do not set `agora_tunnel` and `zrok_share_token` on the same
provider or endpoint. Startup fails because the transport would be
ambiguous.

## CLI

Agora adds these `llm-gateway run` flags:

| Flag | Effect |
|---|---|
| `--network=agora` | Sets `agora.enabled: true` after config load |
| `--agora-integration-file <path>` | Overrides `agora.integration_file` |

`--network=zrok` is accepted, but zrok sharing is still controlled by
the existing `--zrok` flag or `zrok.share.enabled` config.

The `AGORA_LLM_GATEWAY_INTEGRATION_FILE` environment variable sets
`agora.integration_file` when the CLI flag is not provided.

Precedence for the integration file path is:

1. `--agora-integration-file`
2. `AGORA_LLM_GATEWAY_INTEGRATION_FILE`
3. `agora.integration_file`

Values inside the main config override values loaded from the
integration file.

## Integration File

The integration file is a partial `agora:` block, normally produced
by provisioning or demo bootstrap tooling. It carries environment
and catalog IDs while leaving operator choices in the main config.

```yaml
api_endpoint: "http://127.0.0.1:8080"
env_root: "/home/example/.agora/envs/llm-gateway"
advertisement:
  workgroup_ids:
    - wg_abcdefghijkl
  contract_id: con_abcdefghijkl
```

The gateway merges these fields only when the main config leaves the
same field unset:

| Field | Merge rule |
|---|---|
| `api_endpoint` | Used only when `agora.api_endpoint` is empty |
| `env_root` | Used only when `agora.env_root` is empty |
| `advertisement.workgroup_ids` | Used only when no inline workgroup IDs are set |
| `advertisement.contract_id` | Used only when no inline contract ID is set |

## Advertisement

When `agora.enabled: true`, advertisement publishing defaults to
enabled. Set `advertisement.publish: false` to use Agora serve or
provider connects without catalog publication.

When publishing is enabled:

- `advertisement.workgroup_ids` must contain at least one
  `wg_`-prefixed Agora workgroup ID.
- `advertisement.contract_id`, when set, must be a `con_`-prefixed
  Agora contract ID.
- `advertisement.capabilities`, when empty, is derived from gateway
  config.

Derived capabilities are emitted in this order:

| Condition | Capability |
|---|---|
| Always when Agora is enabled | `llm-routing` |
| OpenAI provider has an API key after env expansion | `openai` |
| Anthropic provider has an API key after env expansion | `anthropic` |
| Local provider is configured | `local` |
| Semantic or classifier routing is enabled | `semantic-routing` |
| Agora serve is enabled | `agora-serve` |

## Serving Over Agora

Set `agora.serve.enabled: true` to host the gateway API over Agora.
The normal local HTTP listener still starts. Agora serve forwards
fabric traffic to `serve.backend_target`, or to the active local
listener when `backend_target` is empty.

If `listen` uses port `0`, the gateway passes the actual allocated
listener address to Agora serve.

For `tunnel_mode: http`, `serve.backend_target` must include an
`http://` or `https://` scheme. TCP mode can use a host:port target.

`serve.grants` is passed to Agora as additive access grants. Removing
an email from config does not revoke it; use Agora tooling for
revocation.

## Connecting Providers Over Agora

When a provider or endpoint sets `agora_tunnel`, startup creates an
Agora connect for that tunnel before provider initialization. The
provider then talks to a local loopback address, and Agora forwards
the traffic across the fabric.

Provider keys used internally:

| Config location | Agora connect key |
|---|---|
| `providers.open_ai.agora_tunnel` | `openai` |
| `providers.anthropic.agora_tunnel` | `anthropic` |
| `providers.local.agora_tunnel` | `local` |
| `providers.local.endpoints[].agora_tunnel` | `local:<endpoint name>` |

The local multi-endpoint provider can mix direct HTTP, zrok, and
Agora endpoints in one weighted round-robin pool.

## Lifecycle

When Agora is enabled, startup resolves the effective config, builds
an embedded Agora agent, validates that `api_endpoint` matches the
enrolled environment, and starts the runtime only when needed for
serve or provider connects.

Provider connects are created before provider initialization so their
local listen addresses can be used as provider base URLs. Serving is
started after the gateway HTTP listener is active. Advertisement
publishing happens last so the catalog sees the gateway after its
claimed transports are ready.

On shutdown, the gateway retracts the advertisement, removes serve
and connect resources, closes the Agora agent, and continues cleanup
even if one Agora cleanup step fails.
