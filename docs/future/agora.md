# Agora Integration — Layer-1 Redux

Spec for re-doing the llm-gateway agora integration on the SDK's layer-1 `Dial`/`Listen` primitives. Future-state design; the implementation seams are named, the implementation itself is left to the work order.

## Context

The llm-gateway received a first agora pass that used the SDK's **managed-runtime** API — `EnsureServed` / `EnsureConnected`, `StartRuntime` — which provisions proxy-kind tunnels and reaches them through a **loopback hop**: a local port the runtime forwards to and from the overlay. That work is stranded on `origin/agora-v0.1.0` (commits `d72e1c8`, `29a29fe`); `main` has no agora code at all.

Since then the agora SDK added **layer-1 primitives** (v0.1.3) — raw `Listen` and `Dial` over direct-kind tunnels, similar to what zrok provides — and the sister project **mcp-gateway** already redid its integration on them ("agora redux", `d56dab0`, +1137/−887). This spec redoes llm-gateway's integration the same way... fresh off `main`, mirroring the mcp-gateway pattern, salvaging the reusable pieces of the stranded branch (config shape, identity/capability/integration-file helpers, operator docs). The outcome is a gateway that both serves itself and reaches its providers over agora with no loopback indirection, agora sitting beside zrok as a peer transport.

## Two directions: serve in, dial out

The integration has two independent sides. Keeping them distinct is what makes the rest of the spec read cleanly.

**Serve (clients connect in).** The gateway exposes its own HTTP handler over an agora tunnel.

```
agent  ──[agora]──>  gateway  ──[direct internet]──>  cloud API
```

This is the gateway's front door, and its headline use is the **credential firewall**: the gateway holds the real provider keys, vends virtual keys to agents and users (the existing `api_keys` / `AllowedModels` machinery), and proxies. Agents reach it privately over the overlay; the real keys never leave it. In this mode the gateway calls the cloud API **directly** over the internet — no agora on the outbound side.

**Dial (gateway reaches out).** The gateway reaches a backend that lives behind an agora tunnel.

```
agent  ──>  gateway  ──[agora]──>  backend
```

The thing answering on the far end is **not ours to build** — agora ships it. `agora tunnel serve <name> --mode http --backend <addr> --grant <email>` (a built-in CLI, `cmd/agora/tunnelServe.go`) provisions a tunnel and forwards it to a local `--backend`. It is the generic, non-MCP equivalent of `mcp-bridge`: the operator runs it next to the backend, and the gateway dials the tunnel. The primary dial case is **local/self-hosted inference** (`--backend http://localhost:11434`); the same tool also fronts a cloud API for controlled egress — as a raw TCP passthrough, so the gateway keeps end-to-end TLS (see Dial) — as a secondary case.

The two sides compose freely. A firewall deployment uses serve only; a gateway fronting private inference uses dial only; a deployment can do both.

## Core framing: agora is a peer transport to zrok

llm-gateway already has a transport-injection seam — zrok. The redux exploits it rather than inventing a parallel one:

- Every provider has a `...WithClient(baseURL, *http.Client)` constructor (`NewOpenAIWithClient`, `NewAnthropicWithClient`, `NewLocalWithClient`, and `MultiLocal` via `EndpointOption.HTTPClient`).
- The semantic router's embedding client has `NewEmbedClientWithHTTPClient`.
- Every dial-able config site already carries a `ZrokShareToken` (`OpenAIConfig`, `AnthropicConfig`, `LocalConfig`, `LocalEndpointConfig`).

So the model is simply... wherever zrok plugs in, agora plugs in beside it. The agora subsystem produces `*http.Client`s for dialing and a `net.Listener` for serving; those flow into the seams that already exist. The first pass's loopback port disappears, because layer-1 `Dial` returns a raw `net.Conn` and layer-1 `Listen` returns a real `net.Listener`.

## What the SDK gives us (layer-1)

From `github.com/openziti/agora/sdk/agent/tunnel`, against a **runtime-less** agent (`agent.NewStandalone({WithRuntime: false})`). Iteration 1 uses the bind/dial subset:

- `Get(ctx, a, nameOrID) (*Tunnel, error)` — resolve/validate a pre-provisioned tunnel without opening it.
- `Listen(ctx, a, nameOrID) (net.Listener, error)` — raw overlay listener for an existing direct tunnel. No heartbeat, no managed serve record, no proxy wrapping.
- `Attach(ctx, a, nameOrID) (*Attachment, error)` / `Detach(ctx, a, target) error` — durable dialer attachment (consumer side, a control-plane reservation).
- `Dial(ctx, a, nameOrID) (net.Conn, error)` — raw overlay connection. No local proxy port.
- `EnsurePublished` / `Retract` — catalog advertisement, unchanged from the first pass.

`Create` / `Delete` exist but are **not used in iteration 1** (serve is bind-only — see below).

## Package shape (mirror mcp-gateway)

A new top-level `agora/` package — not the first pass's `gateway/agora*.go` layout. This keeps the two gateways structurally parallel and gives a clean test seam:

- `agora/subsystem.go` — owns the single runtime-less `*agent.Agent`; the `agoraOps` interface wrapping the thin primitives (`Get`, `Listen`, `Attach`, `Dial`, `Detach`, `EnsurePublished`, `Retract`, `Close`) so tests inject a fake; lifecycle.
- `agora/serve.go` — the bind-only serve wrapper.
- `agora/dial.go` — the attach-once dialer producing shared `*http.Client`s.
- `agora/config.go`, `agora/identity.go`, `agora/integration.go` — config types, identity resolution, integration-file merge (salvaged and adapted from the branch).

### Serve (bind-only)

The gateway's own serve tunnel is **operator pre-provisioned**, never created by the gateway. `Subsystem.Serve(ctx)` resolves the configured tunnel by name (`Get`), opens it (`Listen`), and binds the gateway's existing HTTP handler directly to the returned `net.Listener`. No loopback hop. `Close()` only closes the listener; it never deletes a tunnel.

This deletes the create-or-bind foot-guns outright — no create race, no leak-on-crash, no ephemeral grants re-applied each boot. The serve tunnel is a durable, operator-owned resource. The provisioning command and grants live on the operator side, out of the gateway's config.

The gateway's front-door tunnel is a **direct** tunnel (the handler is the in-process backend; there is no `--backend` forwarding, so `agora tunnel serve` is *not* the right provisioner for it). If agora lacks a clean operator path to provision a bindable direct tunnel + grants, that gap is fixed **upstream in the agora project** rather than worked around with a gateway-side create path.

Bind-only is the one deliberate divergence from the mcp-gateway reference, whose `serve.go` is create-or-bind. The implementation should copy mcp-gateway's `subsystem.go` and `dial.go` shape but write `serve.go` fresh — this is the single place "mirror mcp-gateway" does not apply.

### Dial (attach-once, dial-per-conn)

At startup, for each unique `agora_tunnel` referenced in config, `Dialer.Attach(ctx, name)` once — a control-plane reservation kept out of the request hot path. Each attached tunnel gets a shared `*http.Client` whose `Transport.DialContext` returns `tunnel.Dial(ctx, a, name)`. `Dialer.HTTPClient(name)` hands that cached client to consumers.

The dialer swaps **only** the transport: each provider keeps its normal base URL, and just its `Transport.DialContext` is replaced to route over the tunnel (the dialed address is ignored; the tunnel name selects the route). Two cases fall out of that single rule:

- **Opaque / local backend** (e.g. local inference): the base URL's host is cosmetic — the upstream ignores it — so any stable value works, a sentinel like `http://<tunnel-name>` included. Plain HTTP rides the tunnel. This is mcp-gateway's `http://mcp-backend` case.
- **Cloud egress** (a real upstream such as `https://api.openai.com`): keep the *real* `https` base URL. The provider's transport then originates TLS *over* the agora `net.Conn` with correct SNI and `Host`, and `agora tunnel serve` is a raw TCP passthrough to the upstream. TLS is **end-to-end**, gateway to upstream — nothing is decrypted at the relay, a stronger property than the loopback design had.

Per-provider and per-endpoint transports keep each tunnel's connection pool separate.

## Lifecycle and failure

- **Startup failure is fatal (iteration 1).** If the agora controller is unreachable, or a serve bind / dial attach fails at boot, the gateway does **not** start. Resilient degraded-mode startup (boot without agora, retry in the background) is explicitly deferred — see Deferred.
- **Transport precedence: agora wins.** When a provider or endpoint has both `zrok_share_token` and `agora_tunnel` set, the gateway dials it over **agora**. This is a documented rule, not a silent pick. (Serving over zrok and over agora are independent listeners and may both run.)
- **Cleanup.** `Subsystem.Close()` retracts the advertisement, closes the serve listener (no delete — bind-only), detaches every dialer, and closes the agent.

## Integration seams in llm-gateway

The sites the work order will touch, named here for grounding:

- **Config** (`gateway/config.go`): add a top-level `Agora *AgoraConfig` (`enabled`, `integration_file`, `api_endpoint`, `env_root`, `instance_name`, `description`, `serve.{enabled,tunnel}`, `advertisement.{publish,workgroup_ids,contract_id,capabilities}`); add an `AgoraTunnel string` field to `OpenAIConfig`, `AnthropicConfig`, `LocalConfig`, and `LocalEndpointConfig` — exactly paralleling the existing `ZrokShareToken` field. (No `serve.grants` — grants are an operator/provisioning concern under bind-only.)
- **Bootstrap** (`gateway/gateway.go` `New`): construct the agora `Subsystem`; `Attach` every referenced tunnel; build the per-tunnel `*http.Client`s.
- **Provider wiring** (`initProviders` and the local init paths): when a provider or endpoint has `AgoraTunnel` set, construct it via its `...WithClient` constructor with the agora client and sentinel URL, instead of the zrok client or a direct base URL. Agora takes precedence over a zrok token on the same site.
- **Multi-endpoint local — per-endpoint tunnels** (chosen): each `LocalEndpointConfig` maps to its own agora tunnel. This is a **drop-in**: `roundRobinTransport.doWithEndpoint` (multiLocal.go) already selects each endpoint's own `client.Transport` per request — the path that "supports zrok" today — so per-endpoint agora clients slot in unchanged, exactly as zrok already does. No special round-robin composition is needed.
- **Semantic router — free** (`resolveEmbedProvider`, `gateway/gateway.go`): the embedding and classifier layers already inherit the provider's resolved `*http.Client` (`multi.RoundRobinClient()` or the openai client). No new agora config; they ride the provider transport automatically.
- **Serve startup** (`Run`): a `startAgoraServer` alongside the existing local and zrok servers, binding the handler to `Serve(ctx).Listener()`; publish the advertisement.
- **Cleanup**: `Subsystem.Close()` as described under Lifecycle.

## Scenarios

**Credential firewall (primary cloud pattern).** `agora.serve.enabled: true`, `serve.tunnel: llm-gateway` (operator pre-provisioned). Agents reach the gateway over agora and present virtual keys; the gateway holds the real OpenAI/Anthropic keys and dials the cloud **directly** over the internet. The overlay gives private, zero-trust access to the firewall; no provider `agora_tunnel` is involved. The same handler can be reachable over zrok simultaneously.

**Local inference over agora (primary dial pattern).** Three local endpoints, each exposed by its own `agora tunnel serve --mode http --backend http://localhost:11434` running next to the inference server, and each named in a `LocalEndpointConfig.agora_tunnel`. At startup the gateway attaches all three; round-robin balances across the three overlay tunnels; the embedding matcher and LLM classifier reach local inference through the *same* rotating transport, inheriting it via `resolveEmbedProvider` with zero extra config.

**Cloud egress over agora (secondary dial pattern).** `providers.open_ai.agora_tunnel: openai-egress`, where `openai-egress` is an `agora tunnel serve` raw TCP passthrough to `api.openai.com:443`. The provider keeps its real `https://api.openai.com` base URL; its transport originates TLS over the tunnel, so the gateway holds an end-to-end encrypted path to the upstream and the relay sees only ciphertext. Used when the gateway has no direct internet route and must exit through a controlled relay.

## Deferred (and why)

**Managed-create serve (create-or-bind).** Iteration 1 is bind-only; the gateway never provisions its serve tunnel. The create path is deferred, and the bind-provisioning gap — if any — is closed upstream in agora rather than reintroduced here.

**Resilient degraded-mode startup.** Iteration 1 makes agora-at-boot fatal. Booting without agora and reconnecting in the background (so a transient agora outage doesn't take down a gateway also serving non-agora providers) is a deliberate later iteration, decided against observed behavior.

**Relay-side TLS termination / header transforms.** The egress relay is a raw TCP passthrough, so TLS is end-to-end (gateway↔upstream) and there is nothing to terminate. A relay that instead terminates TLS to inject headers, rewrite hosts, or audit payloads is a different shape and out of scope here.

**Single-tunnel relay-side load balancing for local.** Considered and rejected in favor of per-endpoint tunnels, which preserve the gateway's existing load-balancing semantics over the overlay. Revisit only if per-endpoint tunnel sprawl becomes an operational problem.

**UDP/TCP serve modes.** The gateway serves HTTP; serve mode is HTTP. Other modes are SDK-supported but out of scope.

**Steady-state reconnection policy** (heartbeat, retry, re-attach after a mid-run drop). Layer-1 hands lifetime ownership to the gateway; if reconnection turns out to be needed it is designed against observed behavior, not up front.

## Verification (for the eventual implementation)

- **Unit**: a fake `agoraOps` drives the bind-only serve path (resolve + listen, close-without-delete) and the dialer's attach-once plus shared-client reuse — mirroring mcp-gateway's `serve_test.go` and `dial_test.go`. A both-transports-set case asserts agora precedence over zrok.
- **Integration**: a provider built with an agora client reaches a stub server bound to a fake listener; multi-endpoint local round-robins across per-tunnel clients; semantic routing inherits the transport.
- **Manual smoke**: against an enrolled agora environment, expose a local backend with `agora tunnel serve` and dial it from one provider, and serve the gateway over a second pre-provisioned tunnel; confirm a chat completion round-trips and the advertisement appears in the catalog. Mirror the smoke steps in mcp-gateway's `docs/current/agora.md`.
