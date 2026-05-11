# Agora Integration (historical spec)

This document is the original forward-looking spec for Agora-network
support. The implemented operator documentation now lives in
[`../agora.md`](../agora.md), and the matching work order lives in
[`agora-work-order.md`](./agora-work-order.md).

[Agora](https://github.com/openziti/agora) is a zero-trust overlay
network for agent-to-agent communication, built on OpenZiti. It
provides identity, discovery, policy, and communication primitives
for autonomous agents.

In **agora mode**, the gateway operates as a node on an Agora network.
It can do three independent things, each individually togglable:

1. **Publish an advertisement** in the Agora controller's catalog,
   making the gateway discoverable to other agents in the network.
2. **Serve over an Agora Layer 1 tunnel**, accepting client traffic
   through the Agora fabric instead of (or in addition to) the local
   TCP listener.
3. **Connect to providers through Agora Layer 1 tunnels**, reaching
   backend providers across the fabric instead of via direct HTTP or
   zrok.

These three capabilities mirror Agora to the gateway's existing zrok
integration shape: an advertisement is the catalog analog of "what
zrok would call a reserved share," serve is the structural peer of
`zrok.share`, and per-provider connect is the peer of
`zrok_share_token`. Each is additive — none disables the gateway's
existing transports.

## Why agora and zrok coexist as peer transports

The gateway's relationship with `zrok:` is already additive. Setting
`zrok.share.enabled: true` does not disable the local TCP listener —
it adds a zrok share alongside it. Per-provider `zrok_share_token`
does not disable direct HTTP — it routes that one provider's traffic
through the overlay.

Agora mode follows the same shape. Enabling `agora:` does not change
how the gateway listens, how clients reach it, or how the gateway
reaches backends, by default. The three sub-capabilities each layer
on additional functionality, individually:

- **Advertisement only** (the simplest mode): the gateway is visible
  in the Agora catalog but does not move any traffic over Agora.
  Useful for dashboard demos, discovery experiments, and "shallow
  integration" deployments per Agora's dashboard design.
- **Advertisement + serve**: the gateway is discoverable and reachable
  through the Agora fabric. Clients running on Agora can find the
  gateway via the catalog and connect through the served tunnel.
- **Advertisement + serve + per-provider connect**: the gateway is a
  full Agora-native router, with both inbound and outbound traffic
  moving over the fabric.
- Any subset (e.g., serve without an advertisement) is also valid; the
  gateway does not enforce coupling between the layers beyond
  preventing inconsistent configuration. See [§ Coherence](#coherence).

Agora is a **peer transport** to zrok, not a replacement. A deployment
may run both simultaneously: serve over zrok AND over Agora, with
some providers reached via zrok and others via Agora.

## Configuration

The gateway's main YAML config gains an optional top-level `agora:`
block, sibling to `zrok:`. Within that block, the gateway's identity
and transport mode are top-level fields; the serve and connect
features are nested sub-blocks where structurally appropriate.

```yaml
listen: ":8080"

zrok:
  share:
    enabled: false

agora:
  enabled: false                # master switch; see also --network=agora

  # convenience: load any of the fields below from a YAML file, typically
  # produced by Agora's demo-bootstrap. inline values override file values.
  # see "Integration file" below.
  integration_file: ""

  # connection: where the controller is, and which enrolled identity to
  # authenticate as.
  api_endpoint: ""              # e.g., http://127.0.0.1:8080
  env_root: ""                  # path to enrolled environment root;
                                # falls through to AGORA_ENV_ROOT, then ~/.agora

  # identity: the gateway's name and description, used everywhere
  # (advertisement name, serve service name, log scoping).
  instance_name: ""             # defaults to "llm-gateway"
  description: ""               # defaults to "OpenAI-compatible LLM gateway"

  # transport: the gateway's tunnel mode. used by both the advertisement
  # (informational on the catalog card) and the serve (when enabled).
  tunnel_mode: tcp              # tcp | http | udp; default tcp

  # advertisement: what gets published in the Agora catalog.
  # nested for grouping; the advertisement itself is always published
  # when agora.enabled is true. set advertisement.publish: false to
  # disable advertisement publishing while keeping other agora features.
  advertisement:
    publish: true               # default true; the descriptive baseline
    workgroup_ids: []           # required when publish: true; at least one wg_… ID
    contract_id: ""             # optional: con_… ID
    capabilities: []            # auto-derived from providers when empty

  # serve: host the gateway over an Agora Layer 1 tunnel, parallel to
  # zrok.share. when enabled, an EnsureServed call binds the gateway's
  # listen address as the backend target for the tunnel.
  serve:
    enabled: false
    backend_target: ""          # local address to forward fabric traffic to;
                                # defaults to listen
    grants: []                  # operator emails granted access; additive
                                # only — removals require agora CLI

providers:
  open_ai:
    api_key: "${OPENAI_API_KEY}"
    # zrok_share_token: ""      # existing: reach OpenAI via zrok share
    agora_service: ""           # new: reach OpenAI via an Agora Layer 1 service
  anthropic:
    api_key: "${ANTHROPIC_API_KEY}"
    agora_service: ""
  local:
    base_url: "http://localhost:11434"
    agora_service: ""
    # endpoints: [...]          # multi-endpoint mode: each endpoint can
                                # also set zrok_share_token or agora_service
```

Field-by-field semantics follow.

### `enabled`

Master switch. When false (the default), the gateway behaves exactly
as it does today — no Agora SDK initialization, no embedded runtime,
no advertisement publish, no Agora dependency surfaced at runtime.
The `--network=agora` CLI flag sets this to true.

### `integration_file`

Optional path to a YAML file whose fields are merged into this `agora:`
block. The file is loaded after the main config but before final
resolution, and its values are used only for fields not already set
inline. See [§ Integration file](#integration-file).

Precedence: `--agora-integration-file` flag > `AGORA_LLM_GATEWAY_INTEGRATION_FILE`
env var > `agora.integration_file` in main config.

### `api_endpoint`

URL where the Agora controller's REST API is reachable. Required when
`enabled` is true. Accepts `${VAR}` env substitution.

### `env_root`

Path to the enrolled Agora environment root — the directory holding
the gateway's account token and OpenZiti identity material. Falls
back to `AGORA_ENV_ROOT` env var, then `~/.agora`.

### `instance_name`

The gateway's identity on the Agora network. Used as:

- the advertisement name (published in the catalog)
- the serve's service name (when `serve.enabled: true`)
- the log scope tag (`instance=<instance_name>` in dl output)
- the SDK agent name (`agent.NewStandalone(Name: "llm-gateway-<instance_name>", ...)`)

This is **one identity**, used coherently across all three layers.
Default: `"llm-gateway"`. For multi-instance deployments, set
explicitly per instance (e.g., `"engineering-prod"`,
`"engineering-tools"`).

### `description`

One-line summary attached to the advertisement and surfaced in dl
log scope. Default: `"OpenAI-compatible LLM gateway"`.

### `tunnel_mode`

The gateway's transport mode. One of `tcp`, `http`, `udp`. Default:
`tcp`. Used coherently across the layers:

- when the advertisement is published, this is its `tunnel_mode`
  field (matches Agora's `AdvertisementTunnelMode` enum)
- when serve is enabled, this is the serve's mode (the gateway
  speaks the same protocol on the fabric that the advertisement
  advertises)

There is no separate `serve.mode` or `advertisement.tunnel_mode`
field; the gateway has one transport mode for itself, set in one
place. See [§ Coherence](#coherence).

### `advertisement.publish`

Default true when `agora.enabled` is true. Set to false to disable
advertisement publishing while keeping serve and/or per-provider
connect enabled. Useful when an operator wants the gateway on the
network but not in the catalog (e.g., a private internal gateway).

### `advertisement.workgroup_ids`

List of Agora workgroup IDs (each `^wg_[a-z0-9]{12}$`) the
advertisement is visible within. Required, at least one, when
`advertisement.publish` is true. The gateway's account must be a
member of every listed workgroup.

### `advertisement.contract_id`

Optional Agora contract ID (matching `^con_[a-z0-9]{12}$`) the
advertisement binds to.

### `advertisement.capabilities`

List of capability tags. Empty triggers auto-derivation; non-empty
replaces the derived list entirely. See [§ Capability derivation](#capability-derivation).

### `serve.enabled`

When true, the gateway calls `tunnel.EnsureServed` on startup to host
its HTTP API as an Agora Layer 1 service. Clients on the Agora
network can dial the service name (the gateway's `instance_name`) and
reach the gateway through the fabric.

The gateway's local HTTP listener (governed by top-level `listen`)
is independent — it stays up regardless. Agora's serve points at it
as the backend target.

### `serve.backend_target`

Local TCP address that Agora's runtime forwards fabric connections
to. Defaults to the gateway's `listen` value. Operators can override
to bind serve to a loopback-only address (e.g., `127.0.0.1:8080`)
while keeping `listen: 0.0.0.0:8080` for direct LAN access.

### `serve.grants`

List of operator email addresses granted access to the served
tunnel. The list is **additive**: emails added to config are granted
on next start; emails *removed* from config are NOT revoked, because
the underlying agora SDK's `tunnel.EnsureServed` doesn't reconcile
grants (see the SDK spec's "Grant reconciliation" note). Operators
who need to revoke a grant must do so out-of-band via the `agora`
CLI. Operators who prefer fully imperative grant management can
leave this list empty and provision grants entirely via CLI.

When the SDK adds grant reconciliation, this field's semantics
strengthen to full set-assertion without any gateway-side change.

### `providers.<name>.agora_service`

Per-provider field. When set to a non-empty service name, the
gateway calls `tunnel.EnsureConnected` for that service before
initializing the provider, and uses the resolved local listen
address as the provider's `base_url` (or as the dial target for
HTTP, depending on the provider).

This is the structural peer of the existing `zrok_share_token` field.
A provider may have either, neither, or both — if both are set, the
gateway logs an error at startup and exits (per-provider transports
must be unambiguous).

For multi-endpoint providers, each endpoint independently chooses
its transport. A single provider can mix direct, zrok, and agora
endpoints.

## Coherence

The gateway enforces coherence between its agora layers. When two or
more layers are enabled, they share their underlying values rather
than being independently configured. The operator sets one value;
the gateway propagates it.

| Concept    | Source field          | Used by                                    |
| ---------- | --------------------- | ------------------------------------------ |
| Identity   | `instance_name`       | advertisement name, serve service name     |
| Description| `description`         | advertisement description, log scope       |
| Mode       | `tunnel_mode`         | advertisement tunnel_mode, serve mode      |

There is no syntax for setting these to different values for
different layers. An operator who needs the gateway to advertise as
one name and serve as another should run two gateway instances with
different `instance_name`s.

This is opinionated — Agora's data model technically allows the
mismatch — but coherence makes the agora+gateway story clean. A
catalog viewer reading the advertisement sees the same name they'd
dial. A log line tagged with the instance name maps to one
identifiable thing on the network.

The capability list is derived once (auto-derivation or operator-set)
and used only by the advertisement, since serve and connect don't
publish capability metadata.

## Capability derivation

When `agora.advertisement.capabilities` is empty, the gateway emits
the following tags based on the rest of its config:

| Condition                                                  | Tag                |
| ---------------------------------------------------------- | ------------------ |
| always (when `agora.enabled: true`)                        | `llm-routing`      |
| `providers.open_ai.api_key` is set after env expansion     | `openai`           |
| `providers.anthropic.api_key` is set after env expansion   | `anthropic`        |
| `providers.local` is configured (single or multi-endpoint) | `local`            |
| `routing.semantic.enabled: true` OR `routing.classifier.enabled: true` | `semantic-routing` |
| `agora.serve.enabled: true`                                | `agora-serve`      |

The order is fixed (the order in the table). `llm-routing` is
unconditional whenever Agora mode is on.

The new `agora-serve` tag tells consumers that the gateway is not
only discoverable but actively reachable over the Agora fabric.
Catalog viewers can use this to distinguish between gateways that
require external addressing and gateways that can be dialed through
Agora alone.

## CLI flags and environment variables

Agora mode adds two flags to `llm-gateway run`:

| Flag                              | Equivalent config field      | Notes |
| --------------------------------- | ---------------------------- | ----- |
| `--network=agora`                 | `agora.enabled: true`        | shorthand; does not disable other transports |
| `--agora-integration-file <path>` | `agora.integration_file`     | overrides config-file value |

`--network=zrok` (or unset) leaves `agora.enabled` at its config-file
value. The flag is a one-way switch — it can turn agora on but not
off — matching the existing `--zrok` flag's posture.

One environment variable is recognized:

| Env var                                | Effect |
| -------------------------------------- | ------ |
| `AGORA_LLM_GATEWAY_INTEGRATION_FILE`   | sets `agora.integration_file` if not set elsewhere |

Standard Agora SDK env vars (`AGORA_ENV_ROOT`, `AGORA_LOG_LEVEL`) are
honored by the SDK directly when the gateway calls into it.

Standard config-string env var substitution (`${VAR}`) works on every
string field in the `agora:` block.

## Precedence

When the same field has more than one source:

1. CLI flag (`--network=agora`, `--agora-integration-file`)
2. Inline `agora:` block in the main config
3. Integration file (when `integration_file` is set)
4. Built-in defaults

Per-field merge. Inline values win over integration-file values.

## Integration file

The integration file carries the values Agora's demo-bootstrap (or
any other provisioning system) generated for this gateway: the
controller endpoint, the env root, the workgroup ID, the contract
ID. Operator-controlled fields (instance name, description,
capabilities, serve config) stay in the main config.

### Format

The integration file's shape is a **subset of the gateway's `agora:`
config block**. Same field names, same types, same semantics. The
file is interpreted as a partial `agora:` block; missing fields are
treated as unset.

```yaml
# $AGORA_DEMO_ROOT/gateways/llm.yaml
# Produced by Agora's demo-bootstrap.

api_endpoint: http://127.0.0.1:8080
env_root: /home/example/.agora-demo/envs/llm-gateway@gateway-services-org
advertisement:
  workgroup_ids:
    - wg_abcdefghijkl
  contract_id: con_abcdefghijkl
```

Note the nested `advertisement:` mirroring the main config's nesting.

### Producer / consumer contract

The gateway is authoritative on the schema. Bootstrap-generated
fields belong in the file; operator-controlled fields don't.

| Field                                | Belongs in integration file? |
| ------------------------------------ | ---------------------------- |
| `api_endpoint`                       | yes — bootstrap-known         |
| `env_root`                           | yes — bootstrap-generated     |
| `advertisement.workgroup_ids`        | yes — bootstrap-generated IDs |
| `advertisement.contract_id`          | yes — bootstrap-generated ID  |
| `instance_name`                      | **no** — operator decision    |
| `description`                        | **no** — operator decision    |
| `tunnel_mode`                        | **no** — operator decision    |
| `advertisement.publish`              | **no** — operator decision    |
| `advertisement.capabilities`         | **no** — derivable / operator |
| `serve.enabled`                      | **no** — operator decision    |
| `serve.backend_target`               | **no** — operator decision    |
| `serve.grants`                       | **no** — operator decision    |
| `enabled`                            | **no** — CLI flag / main config |
| `integration_file`                   | **no** — would be self-referential |

This split lets the bootstrap regenerate without trampling operator
choices.

### File naming

The path is opaque to the gateway — whatever's passed via flag, env
var, or config gets loaded. The demo-bootstrap convention is
`$AGORA_DEMO_ROOT/gateways/llm.yaml` for this gateway and
`$AGORA_DEMO_ROOT/gateways/mcp.yaml` for the MCP Gateway.

## How it works

When `agora.enabled: true`, the gateway:

1. Resolves the effective `agora:` config (per the precedence rules).
2. Validates required fields. `api_endpoint` is mandatory.
   `advertisement.workgroup_ids` is mandatory when
   `advertisement.publish` is true. Missing required fields abort
   startup before any subsystem starts.
3. Constructs the agora subsystem (described below) and stages it
   alongside the HTTP server.
4. Starts the HTTP listener.
5. Once the listener is up, starts the agora subsystem.
6. On SIGINT/SIGTERM, cancels the shared context. Both the HTTP
   server and the agora subsystem shut down. The agora subsystem
   retracts its advertisement, removes its serves and connects, and
   stops the embedded runtime.

### The agora subsystem

The agora subsystem owns an `*agent.Agent` constructed via
`agent.NewStandalone` (see the
[Agora SDK external-consumers-tunnel spec](https://github.com/openziti/agora/blob/main/docs/sdk/external-consumers-tunnel.md)).
Lifecycle is gateway-driven, not SDK-driven:

```
init   -> agent.NewStandalone(WithRuntime: serve.enabled || any provider has agora_service)
        -> agent.StartRuntime(ctx)   // when WithRuntime
ready  -> for each provider with agora_service:
            tunnel.EnsureConnected(ctx, agent, ConnectSpec{...})
            <- yields a local listen address; pass to provider init
        -> if serve.enabled:
            tunnel.EnsureServed(ctx, agent, ServeSpec{...})
        -> if advertisement.publish:
            catalog.EnsurePublished(ctx, agent, PublishSpec{...})
run    -> <-ctx.Done()
shut   -> if advertisement.publish: catalog.Retract(ctx, agent, advertisementID)
        -> if serve.enabled: tunnel.RemoveServe(ctx, agent, instance_name)
        -> for each connect: tunnel.RemoveConnect(ctx, agent, name, listenAddress)
        -> agent.Close(ctx)
```

Per-provider connects happen **before** provider initialization,
because the resolved local listen address from each connect needs to
flow into the provider's `base_url` (the provider talks to
`http://<local-listen>` and Agora forwards across the fabric).

Serve happens **after** the HTTP listener is up so its
`backend_target` resolves to a real listening port.

Advertisement is published last so the catalog only sees the gateway
once it's fully reachable on whatever transports it claimed.

### Why `NewStandalone`, not `app.Run`

The agora SDK's `app.Run` installs signal handlers, parses
`os.Args`, and calls `dl.Init` globally. The gateway has its own
Cobra-managed signal handling, its own CLI parsing, and its own dl
configuration. `agent.NewStandalone` builds the same `*Agent` without
any of those side effects; the gateway drives lifecycle externally.

This is a prerequisite — the Agora SDK must expose `NewStandalone`
(see the external-consumers-tunnel spec) before this gateway work can
proceed cleanly.

### Failure modes

- **Subsystem construction fails.** `newAgoraSubsystem` returns an
  error (missing required field, integration-file load failure,
  etc.). Startup aborts before the HTTP listener comes up.
- **`StartRuntime` fails.** Aborts before any serve/connect/publish
  work runs. No SDK resources to clean up yet; exit.
- **Per-provider connect fails.** First-attempt error from
  `EnsureConnected`. Subsystem aborts; any connects that came up
  earlier in the walk are removed via the deferred shutdown path;
  exit.
- **Serve fails.** First-attempt error from `EnsureServed`.
  Subsystem aborts; connects are removed via the deferred shutdown
  path; exit. (Advertisement has not been published yet — publish
  happens last in the startup sequence.)
- **Advertisement publish fails.** First-attempt error from
  `EnsurePublished`. Subsystem aborts; serve and connects are
  removed via the deferred shutdown path; exit.
- **Retract / Remove fails on shutdown.** Logged warn-level; the
  subsystem proceeds with the rest of its cleanup. Controller-side
  records age out per Agora's TTL behavior.
- **Steady-state actor failures.** Once startup completes
  successfully, serve and connect actor health is the agora
  runtime's concern — its background retry loops drive recovery,
  observable via `ListServes` / `ListConnects` if the subsystem
  chooses to surface them. The gateway does not actively monitor
  or react during steady-state operation.
- **Integration file referenced but missing.** Startup error before
  the HTTP listener comes up. (A specific case of subsystem
  construction failing.)

### Logging

The gateway uses `df/dl`. The agora subsystem adds two scope fields:
`agent="llm-gateway-<instance_name>"` and `instance="<instance_name>"`.
The Agora SDK's logs inherit the process-wide dl config; both the
gateway's and the SDK's logs interleave on stderr.

`agent.NewStandalone` (per the SDK spec) does NOT call `dl.Init`, so
the gateway's logger configuration is preserved.

### What the catalog sees

When `advertisement.publish: true`, the gateway produces a catalog
card with:

- name: `instance_name`
- description: `description`
- capabilities: the resolved capability tag list (with `agora-serve`
  when serve is enabled)
- workgroup scopes: `advertisement.workgroup_ids`
- contract: `advertisement.contract_id` (if set)
- tunnel mode: `tunnel_mode`
- accent: cyan / sky gradient (the LLM Gateway brand color, applied
  by the dashboard from the gateway's account-organization)

## Prerequisites

Agora mode requires:

- An enrolled Agora environment on the host (`agora enable <token>`
  for standalone, or pre-provisioned for demo).
- A reachable Agora controller.
- The public Agora SDK packages `sdk/agent`, `sdk/agent/catalog`, and
  `sdk/agent/tunnel`, plus the standalone-Agent additions
  (`agent.NewStandalone`, `(*Agent).Close`, etc.) — see the
  [Agora external-consumers spec](https://github.com/openziti/agora/blob/main/docs/sdk/external-consumers.md)
  and
  [external-consumers-tunnel spec](https://github.com/openziti/agora/blob/main/docs/sdk/external-consumers-tunnel.md)
  for the contracts.

## Out of scope

- **Deep integration via Agora sessions.** Routing tool calls as
  Agora envelopes through Agora sessions, with the gateway running a
  session handler. Requires `sdk/agent/session` to receive the
  public-types treatment Agora's external-consumers spec defers. Likely
  a separate gateway slice keyed off a future
  `agora.session.enabled` config field.
- **Multiple advertisements per gateway.** One advertisement per
  gateway instance.
- **Multiple serves per gateway.** One serve per gateway instance,
  hosting the HTTP API. Splitting routes across separate serves is
  out of scope.
- **Dynamic update of the advertisement.** Published once at startup,
  retracted at shutdown. Runtime config changes are not re-published.
- **CLI subcommands for Agora resource management.** Operators use
  `agora` CLI for ad-hoc inspection; the gateway does not grow
  agora subcommands.
- **Daemon-mode Agora consumption.** The gateway always embeds an
  Agora runtime when one is needed (per the SDK spec). It does not
  talk to `agora network start` over gRPC.

## Related documentation

- [`docs/zrok.md`](../zrok.md) — sibling network-layer integration
  this design mirrors structurally
- [`docs/configuration.md`](../configuration.md) — overall config
  schema; the `agora:` block fits into the top-level keys section
- Agora's [`docs/dashboard/design.md`](https://github.com/openziti/agora/blob/main/docs/dashboard/design.md)
  "Gateway Integration" — the cross-repo motivation (the gateway
  goes beyond the dashboard design's "shallow integration" framing)
- Agora's [`docs/sdk/external-consumers.md`](https://github.com/openziti/agora/blob/main/docs/sdk/external-consumers.md)
  — the public catalog SDK surface
- Agora's [`docs/sdk/external-consumers-tunnel.md`](https://github.com/openziti/agora/blob/main/docs/sdk/external-consumers-tunnel.md)
  — the public tunnel SDK surface and standalone Agent construction
- Agora's [`docs/dashboard/work-order.md`](https://github.com/openziti/agora/blob/main/docs/dashboard/work-order.md)
  Track F.1 — the demo-bootstrap work that aligns to the
  integration-file format defined here
