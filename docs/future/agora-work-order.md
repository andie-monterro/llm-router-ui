# Agora Integration — Layer-1 Redux: Work Order

Implementation-shaped translation of `docs/future/agora.md` (the spec) into concrete code changes, grounded in llm-gateway's current code and the mcp-gateway reference. The spec owns the *why* and the design decisions; this work order owns the *what to change, where, and in what order*. Read the spec first.

## Provenance and resolved decisions

The integration mirrors mcp-gateway's "agora redux" (`/home/michael/Repos/nf/mcp-gateway/agora/`), which is already on the SDK's layer-1 primitives and compiles against the agora SDK we target. Three questions were resolved with Michael during planning:

1. **Serve provisioning is operator-side, via the agora CLI (confirmed).** The gateway's front-door tunnel is operator-pre-provisioned and the gateway never creates or deletes it. Agora provides `agora tunnel create` and `agora tunnel delete`, which separate provisioning from bind/dial: the operator creates the direct tcp-mode tunnel out-of-band, and the gateway binds to it. **Bind is account-scoped, not grant-conferred:** the gateway binds a tunnel its **account** owns; grants are for client/dialer access, a separate concern that does not confer bind permission. Current agora additionally requires the tunnel to live in the gateway's own enrolled environment; an upcoming agora update — expected to land before this llm-gateway change releases — relaxes that to any account-owned environment (served from one environment at a time). This closes the provisioning gap the spec flagged and confirms the bind-only choice — serve-side end-to-end smoke is unblocked, nothing is gated. (Unit tests never needed it; they run against a fake `agoraOps`.)
2. **Concurrent listeners.** Serve the same handler over every *enabled* listener at once (zrok + agora can both run), replacing today's mutually-exclusive `Run` (§6). The plain local listener is **opt-in / fallback**, not forced on — it starts only when `cfg.Listen` is explicitly set, or as the fallback when no overlay serves (§6). mcp-gateway already implements the multi-listener pattern (`gateway/backend.go`).
3. **Semantic router is unchanged.** Local embeddings already inherit the provider transport and ride the tunnel for free; OpenAI embeddings are left out of scope, matching zrok's existing behavior (§5).

## Sourcing strategy

| Source | Files | Action |
| --- | --- | --- |
| `mcp-gateway/agora/subsystem.go` | subsystem.go | Copy; **drop `Create`/`Delete`** from `agoraOps` (bind-only). |
| `mcp-gateway/agora/dial.go` | dial.go | Copy nearly verbatim. |
| `mcp-gateway/agora/serve.go` | serve.go | **Write fresh** — bind-only (the one deliberate divergence). |
| `mcp-gateway/agora/{config,identity,integration,capabilities}.go` | same | Copy; adapt defaults + drop `ServeConfig.Grants`. |
| `mcp-gateway/agora/{serve,dial,subsystem}_test.go` | same | Copy; trim fake to bind-only method set. |
| `origin/agora-v0.1.0:gateway/agora_capabilities.go` | — | Port the llm-gateway capability list. |
| stranded `gateway/agora.go` (managed-runtime, loopback, `EnsureServed`/`EnsureConnected`/`StartRuntime`) | — | **Do not copy.** |

The agora SDK surface (confirmed against mcp-gateway's working imports): `agent.NewStandalone(agent.StandaloneOptions{Name, Description, EnvRoot, WithRuntime:false})`; `tunnel.Get/Listen/Attach/Dial/Detach` plus `tunnel.Spec`, `tunnel.ModeTCP`, `tunnel.ErrNotFound`, `tunnel.Tunnel`, `tunnel.Attachment`; `catalog.EnsurePublished/Retract`, `catalog.PublishSpec{...}`, `catalog.TunnelHTTP`, `catalog.Capability`.

---

## 1. New `agora/` package

A new top-level `agora/` package, structurally parallel to mcp-gateway's.

### `agora/subsystem.go` — copy, drop Create/Delete

Copy `mcp-gateway/agora/subsystem.go`. The only structural change is the `agoraOps` interface, which loses the two create-path methods because llm-gateway is bind-only:

```go
type agoraOps interface {
    NewStandalone(agent.StandaloneOptions) (any, error)
    RootAPIEndpoint(any) (endpoint, source string)

    // serve side (bind-only — no Create/Delete)
    GetTunnel(context.Context, any, string) (*tunnel.Tunnel, error)
    Listen(context.Context, any, string) (net.Listener, error)

    // dial side
    Attach(context.Context, any, string) (*tunnel.Attachment, error)
    Dial(context.Context, any, string) (net.Conn, error)
    Detach(context.Context, any, string) error

    // catalog
    EnsurePublished(context.Context, any, catalog.PublishSpec) (*catalog.Advertisement, error)
    Retract(context.Context, any, string) error

    Close(context.Context, any) error
}
```

Drop the `Create` and `Delete` methods from `defaultOps` as well. Keep everything else intact: the runtime-less `agent.NewStandalone({WithRuntime:false})` construction (subsystem.go:169), `validateAgentEndpoint`, `validateConfig`, `StartPublishing` (publishes `TunnelMode: catalog.TunnelHTTP` under the resolved serve-tunnel name), `Dialer()`, and `Close()` (retract → close serve → detach all → close agent). `Close()`'s serve step calls `s.serve.Close(ctx)` which, in the bind-only `Serve` (the `agora/serve.go` subsection below), closes the listener and never deletes.

`SubsystemOptions` keeps `ServeWanted`/`PublishWanted`; the gateway passes the §2 wrapper helpers `cfg.AgoraServeEnabled()` and `cfg.AgoraPublishEnabled()` (the latter already ties publish to serve — see §2), **not** the bare `agora.ServeEnabled`/`AdvertisementPublish`.

### `agora/serve.go` — write fresh (bind-only)

This is the single place "mirror mcp-gateway" does not apply. The bind-only `Serve` has no `tunnel`, no `managed` flag, no `Create`, no `Delete`, no create-race handling:

```go
type Serve struct {
    sub      *Subsystem
    listener net.Listener
    closed   bool
}

// Serve resolves the operator-provisioned serve tunnel and binds to it. The
// tunnel must already exist as a direct, tcp-mode tunnel; the gateway never
// creates it. Any resolution/listen failure is fatal (iteration 1).
func (s *Subsystem) Serve(ctx context.Context) (*Serve, error) {
    name := s.ServeTunnelName()
    if name == "" { return nil, fmt.Errorf("agora serve tunnel name is unresolved") }

    // validate the tunnel exists and is tcp-mode (mirror mcp-gateway's
    // requireTCPMode — Listen itself accepts http-mode, so check explicitly)
    if err := s.requireTCPMode(ctx, name); err != nil { return nil, err }

    listener, err := s.ops.Listen(ctx, s.agent, name)
    if err != nil {
        if errors.Is(err, tunnel.ErrNotFound) {
            return nil, fmt.Errorf("agora serve tunnel '%s' is not provisioned; provision it operator-side (bind-only)", name)
        }
        return nil, fmt.Errorf("listen on agora tunnel '%s': %w", name, err)
    }
    sv := &Serve{sub: s, listener: listener}
    s.serve = sv
    return sv, nil
}

func (sv *Serve) Listener() net.Listener { return sv.listener }

// Close closes the listener only — bind-only never deletes the operator-owned tunnel.
func (sv *Serve) Close(ctx context.Context) error {
    if sv == nil || sv.closed { return nil }
    sv.closed = true
    if sv.listener != nil {
        if err := sv.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) { return err }
    }
    return nil
}
```

Port `requireTCPMode` verbatim from mcp-gateway's serve.go:102 (it uses `GetTunnel`). **Mode vs. Kind note for the implementer and the operator docs:** the front-door tunnel is *direct* (Kind) and *tcp-mode* (Mode); the gateway's `http.Server` binds on the raw `net.Listener`, and the catalog advertisement publishes `TunnelHTTP` — exactly mcp-gateway's split. The operator must provision a direct **tcp-mode** tunnel.

### `agora/dial.go` — copy verbatim

Copy `mcp-gateway/agora/dial.go` as-is. The `Dialer` caches one shared `*http.Client` per tunnel name; `Attach` is idempotent (controller-enforced) and builds the client whose `Transport.DialContext` ignores addr and calls `ops.Dial(ctx, agent, name)` (dial.go:54-60); `HTTPClient(name)` returns the cached client and never attaches; `Close` detaches each once. This is the whole attach-once / dial-per-conn behavior the spec describes.

### `agora/config.go` — copy, drop Grants

Copy `mcp-gateway/agora/config.go`. **Remove `ServeConfig.Grants`** (bind-only: grants are an operator/provisioning concern, per the spec). Resulting types:

```go
type Config struct {
    Enabled         bool
    IntegrationFile string
    APIEndpoint     string
    EnvRoot         string
    InstanceName    string
    Description     string
    Advertisement   *AdvertisementConfig
    Serve           *ServeConfig
}
type AdvertisementConfig struct {
    Publish      *bool
    WorkgroupIDs []string `dd:"workgroup_ids"`
    ContractID   string
    Capabilities []string
}
type ServeConfig struct {
    Enabled bool
    Tunnel  string // bind target; defaults to InstanceName
}
```

Keep `IntegrationFile`/`IntegrationAdvertisement`, `ServeEnabled`, `AdvertisementPublish`, `publishExplicit`, `hasWorkgroupIDs`. Because `Grants` is gone, also drop the `cfg.Serve.Grants` env-expansion loop in `integration.go`'s `expandStrings` (integration.go:80-82).

### `agora/identity.go` — copy, adapt defaults

Copy `mcp-gateway/agora/identity.go`. Change the package defaults to:

```go
defaultInstanceName    = "llm-gateway"
defaultDescription     = "OpenAI-compatible LLM gateway"
defaultAgentNamePrefix = "llm-gateway"
```

`resolveIdentity` and `serveTunnelName` are unchanged (the latter is the single source of truth for the serve-tunnel name and the advertisement Name).

### `agora/integration.go` — copy

Copy `mcp-gateway/agora/integration.go` (`loadIntegrationFile`, `mergeIntegrationFile` blank-fill, `ResolveConfig`, `expandStrings`), minus the `Serve.Grants` loop. `ResolveConfig` expands env, merges the integration file, re-expands.

### `agora/capabilities.go` — copy `Derive`, add llm-gateway extras

Copy mcp-gateway's `Derive(base, extras) []string` (dedupe, first-seen order). Add an llm-gateway capability-extras function, ported from `origin/agora-v0.1.0:gateway/agora_capabilities.go`:

```go
// capabilityExtras derives llm-gateway capabilities from configured providers/routing.
func capabilityExtras(/* gateway.Config view */) []string {
    caps := []string{"openai"|"anthropic"|"local" per configured provider}
    if routing.Semantic.Enabled || routing.Classifier.Enabled { caps = append(caps, "semantic-routing") }
    if agora.ServeEnabled        { caps = append(caps, "agora-serve") }
    return caps
}
```

Base capability is `"llm-routing"`. The bootstrap passes `Derive([]string{"llm-routing"}, capabilityExtras(cfg))` as `SubsystemOptions.Capabilities` (mirroring mcp-gateway's `Derive([]string{"mcp-tools"}, gatewayCapabilityExtras(...))`, backend.go:73). Capability derivation reads gateway-level config, so the extras helper lives in the `gateway` package (or takes a small struct), not `agora` — keep `agora` unaware of llm-gateway provider types.

---

## 2. Config changes — `gateway/config.go`

- Add `Agora *agora.Config` to the top-level `Config` (config.go:8-16), beside `Zrok`.
- Add `AgoraTunnel string` to `OpenAIConfig`, `AnthropicConfig`, `LocalConfig`, `LocalEndpointConfig` (config.go:39-63) — exactly paralleling `ZrokShareToken`.
- In `LoadConfig` (config.go:87), after `MergeYAMLFile`, call `agora.ResolveConfig(cfg.Agora)` (env + integration-file merge), mirroring mcp-gateway's `gateway/config.go:75`.
- Add gateway-config helpers `AgoraServeEnabled()` / `AgoraPublishEnabled()` for the bootstrap and `Run`. `AgoraServeEnabled()` mirrors mcp-gateway (`gateway/config.go:129`). **`AgoraPublishEnabled()` additionally requires serve** — `AgoraServeEnabled() && agora.AdvertisementPublish(cfg.Agora)` — so a dial-only gateway never publishes an advertisement whose name points at a front-door tunnel it does not bind. (Iteration 1; publish-only is a later-iteration concern with no scenario today.) **Distinguish explicit from default:** that silent suppression is correct only when publish is *defaulted* on. If `advertisement.publish: true` is set **explicitly** while serve is off (`agora.publishExplicit(cfg.Agora) && !AgoraServeEnabled()`), the operator asked for something iteration 1 can't honor — fail at boot with a directed error (`"agora.advertisement.publish requires agora.serve.enabled in this iteration"`) rather than silently dropping the request. (mcp-gateway exposes `publishExplicit`.)
- Add a `collectAgoraTunnels(cfg) []string` helper returning the unique, trimmed `AgoraTunnel` values for **only the providers/endpoints `initProviders` will actually initialize** — no phantom attachments. Collect OpenAI/Anthropic only when that provider is configured (the same `APIKey != ""` gate `initProviders` uses); for Local, collect the **endpoint** tunnels when `Local.Endpoints` is non-empty (multi mode) and the single `Local.AgoraTunnel` only otherwise (single mode). This is the set the dialer attaches at startup, and the same set the enablement precondition below keys off.
- **Enablement preconditions (fail-fast).** A per-site `agora_tunnel` or `agora.serve.enabled: true` is meaningless without the subsystem. In `LoadConfig`/validate, two directed checks:
  - **(a) dial side** — if `len(collectAgoraTunnels(cfg)) > 0`, require `cfg.Agora != nil && cfg.Agora.Enabled`, else error (e.g. `"agora_tunnel set on a provider/endpoint requires agora.enabled: true"`). Mirrors mcp-gateway's check (`gateway/config.go:105`); guarantees provider wiring (§4) can assume `g.agoraDial` is non-nil whenever `AgoraTunnel` is set.
  - **(b) serve side (symmetric)** — if `cfg.Agora != nil && cfg.Agora.Serve != nil && cfg.Agora.Serve.Enabled`, require `cfg.Agora.Enabled`, else error (e.g. `"agora.serve.enabled requires agora.enabled: true"`). Without this, a serve-intending config with `agora.enabled` omitted reads `AgoraServeEnabled() == false`, so §6 sees no overlay serve and silently opens the plaintext local `:8080` fallback instead of the intended private front door. Add a small config test for both arms.

---

## 3. Bootstrap — `gateway/gateway.go` `New`

Add an agora subsystem field to `Gateway` (`agora *agora.Subsystem`, plus an `agoraDial func(string) (*http.Client, error)` convenience like mcp-gateway's `agoraDial`). In `New` (gateway.go:31), **before** `initProviders` (the providers need the dial clients), when `cfg.Agora != nil && cfg.Agora.Enabled`:

```go
sub, err := agora.NewSubsystem(agora.SubsystemOptions{
    Config:        cfg.Agora,
    Defaults:      agora.Defaults{InstanceName: "llm-gateway", Description: "OpenAI-compatible LLM gateway", AgentNamePrefix: "llm-gateway"},
    Capabilities:  agora.Derive([]string{"llm-routing"}, capabilityExtras(cfg)),
    ServeWanted:   cfg.AgoraServeEnabled(),
    PublishWanted: cfg.AgoraPublishEnabled(),
})
if err != nil { return nil, err }   // fatal at boot — iteration 1
g.agora = sub
dialer := sub.Dialer()
for _, name := range collectAgoraTunnels(cfg) {
    if err := dialer.Attach(ctx, name); err != nil { return nil, err }  // fatal
}
g.agoraDial = dialer.HTTPClient
```

`New` has no `ctx` today; thread a `context.Background()` (or accept one) for the attach calls. Any failure here is fatal (the spec's iteration-1 rule); the existing `defer g.cleanup()` on error path (gateway.go:36) must also close the subsystem — see §7.

---

## 4. Provider wiring — `initProviders`, `initLocalSingle`, `initLocalMulti`

Precedence per site is **agora > zrok > direct**. At each provider/endpoint, add an agora branch *above* the existing zrok branch. Pattern (OpenAI shown, gateway.go:80):

```go
if g.cfg.Providers.OpenAI.AgoraTunnel != "" {
    client, err := g.agoraDial(g.cfg.Providers.OpenAI.AgoraTunnel)
    if err != nil { return err }
    // pass baseURL through UNCHANGED (empty → the constructor's HTTPS default),
    // exactly like the zrok branch. This preserves end-to-end TLS for the
    // cloud-egress case; the agora branch never rewrites the base URL.
    g.providers[providers.ProviderOpenAI] = providers.NewOpenAIWithClient(apiKey, baseURL, client)
} else if g.cfg.Providers.OpenAI.ZrokShareToken != "" {
    /* existing zrok path */
} else { /* existing direct path */ }
```

The **base-URL rule**: the agora branch **never rewrites the base URL** — it passes the configured value straight to the `...WithClient` constructor (mirroring the zrok branch), and the existing constructor defaults handle the empty case. Two cases follow from that single rule:
- **Opaque / local backend** — the host is cosmetic (the upstream ignores it), so any stable value works: the constructor's default (`http://localhost:11434` for Local) is fine, and an operator may set an explicit cosmetic URL like `http://<tunnel-name>` in config if they prefer. Plain HTTP rides the tunnel.
- **Cloud egress** — the operator keeps the real `https://...` base URL (or leaves it empty so OpenAI/Anthropic default to their real HTTPS endpoint). The transport then originates TLS over the tunnel conn with correct SNI/Host, end-to-end — precisely because it sets `DialContext` (not `DialTLSContext`), so the client layers TLS over the dialed conn. *(Confirmed against the zrok seam and mcp-gateway's identical transport shape.)* The gateway forcing an `http://` sentinel here would break this — hence pass-through, never rewrite.

Apply the same branch to Anthropic (gateway.go:103), `initLocalSingle` (gateway.go:139), and **per-endpoint** in `initLocalMulti` (gateway.go:161 loop):

```go
if ep.AgoraTunnel != "" {
    client, err := g.agoraDial(ep.AgoraTunnel)
    if err != nil { return fmt.Errorf("agora dial for endpoint '%s': %w", ep.Name, err) }
    opt.HTTPClient = client
} else if ep.ZrokShareToken != "" { /* existing */ }
```

This is a true drop-in: `roundRobinTransport.doWithEndpoint` already selects `ep.local.client.Transport` per request (multiLocal.go:370), so per-endpoint agora clients route with no round-robin changes.

**Health-check transport (verify at implementation).** `initLocalMulti` starts per-endpoint health checks (`StartHealthChecks`). Confirm those probes ride each endpoint's own `client` (the agora-wrapped one), not a separate direct dialer — so an agora-tunneled endpoint's health reflects real tunnel+backend reachability (tunnel down → endpoint marked unhealthy and rotated out, the correct behavior). If health probing uses an off-transport client, an agora-only endpoint could read healthy while unreachable; route it through the endpoint client. This is the one behavioral path the plan leaves implicit.

---

## 5. Semantic router — no change

Decision 3. `resolveEmbedProvider` (gateway.go:340) already returns `multi.RoundRobinClient()` for multi-endpoint local and `g.localHTTPClient` for single local — so local embeddings and the classifier inherit the agora transport automatically once the local provider is built over agora. **No agora config and no code change** in the semantic router. Leave the `openai` branch (gateway.go:359) returning a nil httpClient as-is: OpenAI semantic-routing calls — **both embeddings and the classifier**, which resolve through this same branch (`resolveEmbedProvider` is called for the classifier too, gateway.go:329) — do not ride any provider transport today (zrok has the identical gap), so OpenAI semantic routing over agora is explicitly out of scope. Note this limitation in the operator docs.

---

## 6. Serve startup — restructure `Run` (concurrent listeners)

Decision 2. Today's `Run` (gateway.go:207) picks `runWithZrok` **or** `runLocal`. Replace that either/or with the multi-listener pattern mcp-gateway already uses (`gateway/backend.go:285-316`, helpers `serveHTTP` at :383 and `shutdownHTTPServers` at :394). New shape:

1. Build the handler once (`g.newHandler()`).
2. Assemble `servers []*http.Server` + per-server `net.Listener`, one per **enabled** transport:
   - **Local TCP — opt-in / fallback (the rule, no longer "decide").** Start the local `:8080`-style listener only when **either** `cfg.Listen` is explicitly set **or** no overlay serve is enabled (neither `cfg.Zrok.Share.Enabled` nor `cfg.AgoraServeEnabled()`). This keeps the credential-firewall deployment private-only: enable agora serve, leave `Listen` unset, and no plaintext local port is opened. The fallback arm still guarantees at least one listener for a plain (no-overlay) deployment. **Change today's "empty → `:8080`" default** (gateway.go:233): default to `:8080` *only* in the fallback arm; when an overlay serves and `Listen` is empty, do not start a local listener. Build via `net.Listen("tcp", addr)` fed through the same `serveHTTP` path for uniformity.
   - **Zrok** — when `cfg.Zrok.Share.Enabled`: build the share (existing `NewShare`/`NewShareFromToken`, gateway.go:262-267), `server.Serve(share.Listener())`.
   - **Agora** — when `cfg.AgoraServeEnabled()`: `serve, err := g.agora.Serve(ctx)`; `server.Serve(serve.Listener())`.
3. After assembling the listeners, create the shared `errCh` with capacity `len(servers)` (llm-gateway can have 3 listeners vs. mcp-gateway's 2, so a fixed-size-2 buffer would let a later goroutine block forever on send during shutdown — a goroutine leak). Then start each listener with a `serveHTTP(server, listener, errCh, label)` goroutine feeding that channel (port the helper).
4. If `cfg.AgoraPublishEnabled()`: `g.agora.StartPublishing(ctx)` after listeners are up (mirror backend.go:300); a publish failure tears down all servers.
5. `select { case <-ctx.Done(): shutdownAll; case err := <-errCh: shutdownAll; return err }`.
6. Error if `len(servers) == 0`.

`runLocal`/`runWithZrok` collapse into this assembly. Keep the existing signal handling and `defer g.cleanup()` (gateway.go:210-222).

---

## 7. Cleanup — `gateway/gateway.go` `cleanup`

In `cleanup` (gateway.go:295), after the provider/share/access teardown, add:

```go
if g.agora != nil {
    if err := g.agora.Close(); err != nil {
        dl.Warnf("error closing agora subsystem: %v", err)
    }
}
```

`Subsystem.Close()` retracts the advertisement, closes the serve listener (no delete — bind-only), detaches every dial tunnel, and closes the agent (subsystem.go:306). This runs both on the `New` error path (via the existing `defer g.cleanup()`, gateway.go:38) and on normal `Run` shutdown.

---

## 8. Slicing (suggested commit order)

1. Vendor the `agora/` package: config, identity, integration, capabilities, subsystem (Create/Delete dropped), dial, serve (fresh) + their tests. Compiles and tests green in isolation.
2. Config: `Agora *agora.Config`, per-site `AgoraTunnel`, `ResolveConfig` call, helpers, `collectAgoraTunnels`.
3. Bootstrap + provider wiring (§3, §4) behind `cfg.Agora.Enabled`.
4. `Run` restructure to concurrent listeners (§6) + cleanup (§7).
5. Operator docs (§10) + CHANGELOG `## Unreleased` entry (in-house format: one `FEATURE` line).

---

## 9. Verification

**Unit (no controller; fake `agoraOps`).** Port mcp-gateway's `fakeOps`, trimmed to the bind-only method set (drop the Create/Delete recordings):
- `serve_test.go` — resolve + listen binds an existing tcp-mode tunnel; `Close` closes the listener and **does not delete**; a missing tunnel (`ErrNotFound`) and a non-tcp tunnel each error.
- `dial_test.go` — attach-once per name; `HTTPClient` performs no attach; `Close` detaches each once; `DialContext` routes through `Dial(name)` ignoring addr (copy mcp-gateway's `dial_test.go` directly).
- `subsystem_test.go` — runtime-less agent (`WithRuntime:false`); publishes `TunnelHTTP` under the serve-tunnel name; `Close` order is retract → detach → close (**no delete**).
- **Dial-only no-publish** — a config with `agora.enabled:true`, `serve.enabled:false`, and publish *defaulted* on (even with workgroup IDs present) publishes **nothing** (`AgoraPublishEnabled()` is false, so `PublishWanted` is false and no advertisement is created). Conversely, `serve.enabled:false` with `advertisement.publish:true` set **explicitly** fails validation with the directed error (loud, not silent).
- **Precedence** — a provider config with *both* `agora_tunnel` and `zrok_share_token` builds over agora (assert the provider's client is the agora dial client); and a `LocalEndpointConfig` with *both* set likewise uses the agora client for that endpoint — asserting the per-endpoint invariant directly, not by analogy. Lives in the `gateway` package.

**Integration.** A provider built with an agora dial client reaches a stub `httptest` server bound to a fake in-memory listener (the fake `Dial` returns one end of a `net.Pipe`); multi-endpoint local round-robins across per-tunnel clients; semantic routing inherits the local transport.

**Manual smoke (enrolled agora environment).**
- *Dial (buildable now):* expose a local backend with `agora tunnel serve <name> --mode http --backend http://localhost:11434`, set a `LocalEndpointConfig.agora_tunnel: <name>`, and confirm a chat completion round-trips over the overlay.
- *Serve:* provision the front-door tunnel out-of-band with `agora tunnel create <name>` (a direct tcp-mode tunnel) within the gateway's **account** — and, on current agora, in the gateway's own enrolled environment — then grant the *clients* that should reach it (grants are dialer access, not bind permission; exact flags belong in the operator docs). Set `agora.serve.enabled: true` / `serve.tunnel: <name>`, and confirm the handler is reachable over agora and — when `advertisement.workgroup_ids` are configured (directly or via an integration file) — the advertisement appears in the catalog. (Without workgroup IDs, publishing is intentionally skipped, so the catalog check requires them.) `agora tunnel delete <name>` tears it down. Run against the agora version in hand. Mirror mcp-gateway's `docs/current/agora.md` smoke steps.

---

## 10. Operator docs

This repo's built-behavior docs live under **`docs/current/`** (e.g. `docs/current/zrok.md`, `docs/current/multi-endpoint.md`). Author a new **`docs/current/agora.md`** beside `docs/current/zrok.md` at implementation time, mirroring mcp-gateway's `docs/current/agora.md` structure: prerequisites, config reference (`agora.*` block + per-provider `agora_tunnel`), integration file, advertisement (tri-state publish — **`workgroup_ids` required to publish**, via config or integration file — derived capabilities), serving over agora (**bind-only**: the operator pre-provisions a direct tcp-mode tunnel in the gateway's account via `agora tunnel create` / `agora tunnel delete`; the gateway binds a tunnel its account owns and never creates or deletes it. Document grants separately as client/dialer access, not bind permission. Note the environment rule: current agora requires the tunnel in the gateway's enrolled environment, with an upcoming update relaxing this to any account-owned environment, served one at a time), the three scenarios (credential firewall / local inference / cloud egress; in the credential-firewall example **omit `listen`** so no plaintext local port opens, noting that setting `listen` opts into a local listener alongside the overlays), transport precedence (agora > zrok), the OpenAI semantic-routing-over-agora limitation (embeddings *and* classifier; §5), lifecycle (fatal-at-boot), and manual smoke. Adapt the stranded branch's `docs/agora.md` for prose, stripped of all loopback/managed-runtime language.

Also extend the existing docs the way zrok is already documented: add the `agora_tunnel` field and the `agora.*` block to `docs/current/configuration.md`, and the per-endpoint `agora_tunnel` case to `docs/current/multi-endpoint.md`. Add a `docs/current/agora.md` entry to the README docs list.
