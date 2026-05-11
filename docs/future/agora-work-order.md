# Agora Integration — Work Order

This is the implementation plan for the historical spec in
[`agora.md`](./agora.md). The current operator-facing documentation is
[`../agora.md`](../agora.md). This document remains as build-order and
acceptance history.

## Cross-repo posture

The gateway ships agora-mode against contracts the spec defines.
**Two prerequisite contracts on the agora repo must land first:**

1. `sdk/agent/catalog` — already in place. See agora's
   [external-consumers spec](https://github.com/openziti/agora/blob/main/docs/sdk/external-consumers.md).
2. `sdk/agent/tunnel` + `agent.NewStandalone` + `(*Agent).Close` —
   proposed. See agora's
   [external-consumers-tunnel spec](https://github.com/openziti/agora/blob/main/docs/sdk/external-consumers-tunnel.md).

The gateway implementation **cannot start cleanly** until the second
prerequisite is in place. `agent.NewStandalone` is the load-bearing
piece: without it, the gateway has to use `app.Run`, which collides
with the gateway's Cobra CLI, signal handling, and dl logger.

Agora's `demo-bootstrap` (Track F.1 of Agora's dashboard work-order)
aligns to the integration-file contract from the gateway side
afterward. The demo-bootstrap landing does not block the gateway's
landing — the gateway can be exercised with hand-written integration
files (or fully inline config) for testing.

## Prerequisites

Before W1 starts, all must be true:

- Agora's `sdk/agent/catalog` package exists with `EnsurePublished`,
  `Retract`, `Lookup`, `List` and public types. **(Already landed.)**
- Agora's `sdk/agent/tunnel` package exists with `EnsureServed`,
  `EnsureConnected`, `RemoveServe`, `RemoveConnect`, `ListServes`,
  `ListConnects` and public types per the external-consumers-tunnel
  spec. **(Pending.)**
- Agora's `agent.NewStandalone`, `(*Agent).StartRuntime`,
  `(*Agent).StopRuntime`, `(*Agent).Close` exist per the same spec.
  **(Pending.)**
- Agora has a tagged release or known-good commit reachable via
  `go get`. Local-replace fallback acceptable for co-development.

## Work units

Fifteen units, sequenced with explicit dependencies. W14 (smoke
check) and W15 (docs publication) are strictly last.

```
W1  add agora dependency
W2  AgoraConfig types & sub-types
W3  integration-file loader
W4  capability derivation
W5  identity resolution & coherence
W6  agora subsystem core (advertisement)
W7  serve subsystem
W8  per-provider connect plumbing
W9  Gateway wiring
W10 CLI flags
W11 example config update
W12 unit tests (parallel-eligible from W2 onward)
W13 provider integration tests (parallel-eligible from W8)
W14 integration smoke
W15 publish docs / README (last)
```

Dependency graph:

```
W1 ──► W2 ──┬──► W3 ──┐
            ├──► W4 ──┤
            ├──► W5 ──┤
            ├────────►├──► W6 (advertisement) ──┐
            │         │                          │
            │         ├──► W7 (serve) ───────────┼──► W9 (gateway wiring) ──► W10 (CLI) ──► W14 ──► W15
            │         │                          │
            │         └──► W8 (per-provider) ────┘
            │
            └──► W11 (example config; parallel)
                 W12 (unit tests; parallel from W2)
                 W13 (provider tests; parallel from W8)
```

---

### W1 — Add Agora as a dependency

**Goal.** `go.mod` declares `github.com/openziti/agora` as a direct
dependency. `go build ./...` succeeds.

**Work.**

- `go get github.com/openziti/agora@<rev>` against a build that
  includes `sdk/agent/tunnel` and `agent.NewStandalone`.
- Verify `go.sum` updates and `go build ./...` succeeds.
- If co-developed locally, add `replace github.com/openziti/agora =>
  /home/michael/Repos/nf/agora` with a `// TODO: remove before
  release` comment. The replace directive must not survive to a
  tagged gateway release; W15 verifies removal.

**Acceptance.**

- `go build ./...` clean.
- `go vet ./...` clean.
- A throwaway test file that imports `agent`, `catalog`, and
  `tunnel` compiles. (Confirms the prerequisite packages exist.)

---

### W2 — Define agora config types

**Goal.** `gateway/config.go` exposes Go types matching the spec's
config schema, parseable from YAML via `dd.MergeYAMLFile`.

**Work.**

Add to `gateway/config.go`:

```go
type AgoraConfig struct {
    Enabled         bool
    IntegrationFile string

    APIEndpoint string
    EnvRoot     string

    InstanceName string
    Description  string
    TunnelMode   string  // tcp | http | udp

    Advertisement *AgoraAdvertisementConfig
    Serve         *AgoraServeConfig
}

type AgoraAdvertisementConfig struct {
    Publish      *bool    // pointer so default-true is distinguishable
    WorkgroupIDs []string
    ContractID   string
    Capabilities []string
}

type AgoraServeConfig struct {
    Enabled       bool
    BackendTarget string
    Grants        []string
}
```

Add `Agora *AgoraConfig` to the existing `Config` struct.

Add `AgoraService string` to each existing provider config struct
(OpenAI, Anthropic, Local — and to the per-endpoint config struct
for multi-endpoint Local).

Define a separate type for integration-file parsing:

```go
type AgoraIntegrationFile struct {
    APIEndpoint string
    EnvRoot     string

    Advertisement *AgoraIntegrationAdvertisement
}

type AgoraIntegrationAdvertisement struct {
    WorkgroupIDs []string
    ContractID   string
}
```

**Notes.**

- `Advertisement.Publish` is a pointer (`*bool`) so the gateway can
  distinguish "operator left it unset" (treat as true when
  `agora.enabled`) from "operator explicitly set false."
- All optional blocks use the pointer convention matching the rest
  of the config (`Zrok`, `Routing`, etc.).
- Verify `dd` mapping for acronyms (`APIEndpoint` → `api_endpoint`,
  `WorkgroupIDs` → `workgroup_ids`). Add tags only on fields where
  `dd` produces incorrect snake-case.

**Acceptance.**

- `gateway/config.go` declares all the new types.
- `LoadConfig` succeeds on configs without `agora:` (existing tests
  pass unchanged).
- `TestLoadConfig_AgoraBlock` asserts a populated `agora:` config
  parses correctly, including the nested `advertisement:` and
  `serve:` sub-blocks.
- `TestLoadConfig_ProviderAgoraService` asserts
  `providers.open_ai.agora_service` parses.

**Depends on.** W1.

---

### W3 — Integration-file loader

**Goal.** Load `AgoraIntegrationFile` from a path and merge its
values into `AgoraConfig` only where the latter is unset.

**Work.**

Add `gateway/agora_integration.go`:

```go
func loadAgoraIntegrationFile(path string) (*AgoraIntegrationFile, error)
func mergeAgoraIntegrationFile(cfg *AgoraConfig, file *AgoraIntegrationFile)
```

Per-field merge rules:

| Path                                  | Merge rule                              |
| ------------------------------------- | --------------------------------------- |
| `APIEndpoint`                         | take from file iff cfg.APIEndpoint == "" |
| `EnvRoot`                             | take from file iff cfg.EnvRoot == ""    |
| `Advertisement.WorkgroupIDs`          | take from file iff len(cfg.Advertisement.WorkgroupIDs) == 0 (alloc Advertisement if nil) |
| `Advertisement.ContractID`            | take from file iff cfg.Advertisement.ContractID == "" |

Merging into `cfg.Advertisement` requires allocating an empty
`AgoraAdvertisementConfig` if the operator didn't supply one. This
is fine; the merge is idempotent.

**Acceptance.**

- `loadAgoraIntegrationFile` returns parsed file or wrapped error.
- `mergeAgoraIntegrationFile` is pure (no I/O, no globals).
- Unit tests: load success; missing file (error); merge with empty
  cfg; merge with fully-set cfg; merge with partial cfg.

**Depends on.** W2.

---

### W4 — Capability derivation

**Goal.** A function that returns the auto-derived capability tag
list per the spec's table.

**Work.**

Add `gateway/agora_capabilities.go`:

```go
func deriveAgoraCapabilities(cfg *Config) []string
```

Implement the spec's rule exactly. Order is fixed:
`["llm-routing", "openai", "anthropic", "local", "semantic-routing", "agora-serve"]`.

- Apply `os.ExpandEnv` on provider API keys before emptiness check.
- `agora-serve` is conditional on `cfg.Agora != nil && cfg.Agora.Serve != nil && cfg.Agora.Serve.Enabled`.

**Acceptance.**

- Unit tests cover every condition row. Order matches the spec.
- An empty result is impossible — `llm-routing` is always emitted
  when Agora mode is on.

**Depends on.** W2.

---

### W5 — Identity resolution and coherence

**Goal.** A function that resolves the gateway's effective identity
fields from the agora config, applying defaults. Enforces no
inconsistent state.

**Work.**

Add `gateway/agora_identity.go`:

```go
type AgoraIdentity struct {
    InstanceName string  // resolved; never empty
    Description  string  // resolved; never empty
    TunnelMode   string  // resolved; one of tcp/http/udp
    AgentName    string  // "llm-gateway-" + InstanceName
}

func resolveAgoraIdentity(cfg *AgoraConfig) (AgoraIdentity, error)
```

Default resolution:

- `InstanceName`: `cfg.InstanceName` if non-empty, else `"llm-gateway"`.
- `Description`: `cfg.Description` if non-empty, else
  `"OpenAI-compatible LLM gateway"`.
- `TunnelMode`: `cfg.TunnelMode` if non-empty, else `"tcp"`. Validate
  against the allowed set; error on unknown value.
- `AgentName`: always `"llm-gateway-" + InstanceName`.

This function is the single source of truth for the gateway's
identity on Agora. Everything downstream (advertisement spec, serve
spec, log scope) reads `AgoraIdentity`.

**Acceptance.**

- Unit tests cover defaults, explicit values, invalid tunnel_mode.

**Depends on.** W2.

---

### W6 — Agora subsystem core (advertisement)

**Goal.** A self-contained subsystem that owns the `*agent.Agent`
lifecycle and the advertisement publish/retract logic.

**Work.**

Add `gateway/agora.go`:

```go
type agoraSubsystem struct {
    cfg      *AgoraConfig
    identity AgoraIdentity
    derived  []string

    agent *agent.Agent

    // populated post-publish/serve/connect:
    advertisement *catalog.Advertisement
    serveStatus   *tunnel.ServeStatus
    connects      map[string]*tunnel.ConnectStatus  // keyed by provider/endpoint name

    log *dl.Builder
}

func newAgoraSubsystem(cfg *Config) (*agoraSubsystem, error)
func (s *agoraSubsystem) Run(ctx context.Context) error
func (s *agoraSubsystem) ConnectAddress(providerKey string) (string, bool)
```

Construction (`newAgoraSubsystem`) does:

1. Resolve identity (W5).
2. Compute derived capabilities (W4) if
   `cfg.Agora.Advertisement.Capabilities` is empty.
3. Decide `wantRuntime`: true if
   `cfg.Agora.Serve != nil && cfg.Agora.Serve.Enabled` OR any
   provider has a non-empty `agora_service`.
4. Construct `agent.NewStandalone(StandaloneOptions{
     Name: identity.AgentName,
     Description: identity.Description,
     EnvRoot: cfg.Agora.EnvRoot,
     WithRuntime: wantRuntime,
   })`.
5. Validate required fields (api_endpoint, workgroup_ids if
   advertisement.publish, etc.). Errors at this stage abort gateway
   startup.

`Run(ctx)` does the full lifecycle per the spec's "agora subsystem"
diagram. Order:

1. `agent.StartRuntime(ctx)` if runtime is wanted.
2. For each provider with `agora_service`:
   - `tunnel.EnsureConnected(ctx, agent, ConnectSpec{...})`
   - Store resolved listen address in `s.connects[providerKey]`.
3. If `cfg.Agora.Serve.Enabled`:
   - `tunnel.EnsureServed(ctx, agent, ServeSpec{...})`
   - `serve.BackendTarget` defaults to `cfg.Listen` if unset.
4. If `advertisement.publish` (default true):
   - `catalog.EnsurePublished(ctx, agent, PublishSpec{...})`
5. Block on `<-ctx.Done()`.
6. Shutdown order: retract advertisement → remove serve → remove
   each connect → `agent.Close(ctx)`. Each step logged; failures
   logged warn-level and skipped.

`ConnectAddress(providerKey)` returns the resolved local listen
address for a given provider's connect (used by provider init in
W8). Returns `("", false)` if the provider has no connect.

Use `df/dl` scoped with `agent=identity.AgentName` and
`instance=identity.InstanceName`.

**Notes.**

- Per-provider connect timing: connects must complete **before**
  provider init reads `ConnectAddress`. W9 handles the ordering
  between subsystem startup and provider init.
- Retract on shutdown gets a fresh bounded context (5s) so an
  already-cancelled shutdown context doesn't prevent cleanup.

**Acceptance.**

- Unit tests: construct with valid/invalid configs; lifecycle
  against fake catalog and tunnel surfaces; failure-mode paths
  matching the spec's lifecycle ordering (connect fails → earlier
  connects torn down; serve fails → connects torn down; publish
  fails → serve and connects torn down).
- The subsystem never returns from Run with the advertisement
  published AND no retract attempted.

**Depends on.** W2, W3, W4, W5.

---

### W7 — Serve subsystem

**Goal.** Build the `tunnel.ServeSpec` and call `EnsureServed`.

**Work.**

This is logically part of the subsystem's `Run` method (W6) but
deserves its own work-unit because of the lifecycle subtleties.

Inside the subsystem:

```go
func (s *agoraSubsystem) runServe(ctx context.Context, listenAddress string) error
```

Build spec:

- `Name`: `identity.InstanceName`
- `Mode`: `identity.TunnelMode` cast to `tunnel.Mode`
- `BackendTarget`: `cfg.Agora.Serve.BackendTarget` if non-empty, else `listenAddress`
- `GrantEmails`: `cfg.Agora.Serve.Grants`

Call `tunnel.EnsureServed`. Capture `*tunnel.ServeStatus` into
`s.serveStatus` on success. Log resolved fields (serve ID, tunnel
ID, state). The `ServeStatus` is observational — kept for logging
and any later inspection — and is not the key used for removal.

On shutdown, call `tunnel.RemoveServe(ctx, agent, s.identity.InstanceName)`.
The agora SDK's tunnel package keys removal on the stable desired
name (the same `Name` that was passed to `EnsureServed`), not on
the ephemeral `ServeID` — which is necessary because the runtime
clears `ServeID` whenever the actor lands in `StateError`.

**Acceptance.**

- Unit test against fake `EnsureServed` verifies spec construction
  (defaults applied correctly when fields empty).
- Test verifies `RemoveServe` is called with `identity.InstanceName`
  on shutdown, regardless of the captured ServeStatus's state.
- Test verifies that if `EnsureServed` fails, `s.serveStatus` is
  not populated, no `RemoveServe` is attempted, and the failure
  propagates up to abort startup.

**Depends on.** W6.

---

### W8 — Per-provider connect plumbing

**Goal.** Each provider with `agora_service` set gets its connect
established before provider init runs, and the provider's
`base_url` is set to the resolved local address.

**Work.**

Two pieces.

**Piece 1 — connect establishment** (inside the subsystem,
called from `Run`):

```go
func (s *agoraSubsystem) runConnects(ctx context.Context, cfg *Config) error
```

Walk the provider config. For each provider with a non-empty
`AgoraService`:

- Validate: error if both `agora_service` AND `zrok_share_token`
  are set on the same provider/endpoint.
- **Pre-allocate a free loopback port.** Open
  `net.Listen("tcp", "127.0.0.1:0")`, read back
  `listener.Addr().(*net.TCPAddr)`, close the listener, retain the
  resolved `host:port`. There is a microsecond race window between
  the close and the runtime's re-bind in EnsureConnected; acceptable
  for typical use because the gateway controls its loopback.
  (Background: the agora tunnel SDK doesn't echo back a
  kernel-assigned port — see the SDK spec's "Kernel-assigned connect
  ports" note. When agora adds `ResolvedListenAddress`, this step
  collapses to a plain `127.0.0.1:0` in the spec.)
- Build `tunnel.ConnectSpec{Name: agoraService, ListenAddress: "<pre-allocated host:port>"}`.
- Call `tunnel.EnsureConnected`. Store `*tunnel.ConnectStatus` in
  `s.connects[providerKey]` where `providerKey` uniquely identifies
  the provider (or per-endpoint key for multi-endpoint Local). The
  `ConnectStatus` carries the resolved `ListenAddress` (echoed
  back from the spec) and the agora service `Name` — together
  these are the stable identity used for shutdown removal.

On shutdown, for each entry in `s.connects`, call
`tunnel.RemoveConnect(ctx, agent, status.Name, status.ListenAddress)`.
The SDK keys removal on `Name + ListenAddress` (the same key the
runtime upserts on), not on the ephemeral `AttachmentID`, because
the runtime clears `AttachmentID` when an actor lands in
`StateError`.

A small helper alongside `runConnects`:

```go
func allocateLoopbackPort() (string, error)
```

returns a `"127.0.0.1:<port>"` string and an error if allocation
fails. Used per connect.

The key format: `"openai"`, `"anthropic"`, `"local"` for top-level
providers; `"local:<endpoint-name>"` for per-endpoint.

**Piece 2 — provider integration**:

Provider init (`initProviders` in the existing gateway code) needs
to be updated. For each provider/endpoint with `agora_service`:

- After the subsystem's connects are established, fetch
  `s.ConnectAddress(providerKey)`.
- Override the provider's effective `base_url` with
  `"http://" + connectAddress`.
- Continue init normally — the provider's HTTP client now talks
  to the local Agora-provided listen, which forwards across the
  fabric.

**Notes.**

- Per-endpoint connects (multi-endpoint Local) make the connect map
  potentially large. No problem — each connect is independent.
- The subsystem must complete all connects **before** provider init
  uses their addresses. W9 wires the ordering.
- For multi-endpoint Local, the health-check loop (existing
  `health_check`) should treat the connect-fronted endpoint exactly
  like a direct one. Verify nothing breaks.

**Acceptance.**

- Unit test: provider config with one openai provider + agora_service
  produces one EnsureConnected call with the right Name and a
  pre-allocated `127.0.0.1:<port>` ListenAddress, captures the
  matching ConnectStatus.
- Unit test: `allocateLoopbackPort` returns a free, concrete
  `127.0.0.1:<port>` and a nil error in the happy path.
- Unit test: error when both `agora_service` and `zrok_share_token`
  set on same provider.
- Provider init test: provider initialized with an agora_service
  uses `http://127.0.0.1:<pre-allocated-port>` as base_url.

**Depends on.** W6.

---

### W9 — Gateway wiring

**Goal.** The agora subsystem is started before provider init reads
connect addresses, alongside the HTTP server, and shares the
context-cancel teardown.

**Work.**

Update `gateway/gateway.go`. Add `agora *agoraSubsystem` field to
`Gateway`.

In `New(cfg *Config)`:

1. If `cfg.Agora == nil || !cfg.Agora.Enabled`: skip agora entirely.
   No subsystem, no Agora dependency exercised.
2. Else: call `newAgoraSubsystem(cfg)`. Errors abort `New`.

In `Gateway.Run()`:

1. Start the HTTP listener (existing).
2. If `g.agora != nil`:
   a. Start the subsystem's runtime and run **its connect
      establishment** before provider init. Specifically:
      - Call `g.agora.bootstrapConnects(ctx, cfg)` (a new method
        that does steps 1–2 of the subsystem's Run: start runtime,
        establish all connects).
      - This blocks until connects are up.
   b. Now call `initProviders(cfg, g.agora)` — providers can read
      `g.agora.ConnectAddress(...)` to learn their local addresses.
   c. Start the rest of the subsystem's lifecycle in a goroutine:
      `go g.agora.runRemainder(ctx)` — serves, advertisement,
      blocking on ctx.Done(), then cleanup.

The split between `bootstrapConnects` and `runRemainder` exists
because connects must complete before provider init reads their
addresses, but the rest of the lifecycle (serve, advertisement,
block) is independent of provider state.

Alternative simpler shape: keep all of agora subsystem startup
synchronous before provider init, run nothing in a goroutine
during startup, then transition to a single goroutine that blocks
on ctx.Done() and handles cleanup. This is cleaner but means
serve and advertisement startup also block gateway startup — only
relevant if those operations have meaningful latency. For agora
controller calls in a typical demo, this is negligible.

**Pick the simpler shape** unless profile data shows otherwise.

**Acceptance.**

- Agora disabled: gateway behaves exactly as today.
- Agora enabled, valid config, controller reachable: gateway
  starts, providers initialize against agora connect addresses,
  serve is up, advertisement is in catalog.
- SIGINT retracts advertisement, removes serve, removes connects,
  stops runtime; HTTP server drains; clean exit within shutdown
  budget.
- Invalid agora config: `New` returns an error, no partial
  initialization.

**Depends on.** W7, W8.

---

### W10 — CLI flags

**Goal.** `--network=agora` and `--agora-integration-file` flags
exposed on `llm-gateway run`. Env var honored.

**Work.**

Update `cmd/llm-gateway/run.go`:

- Add `network` and `agoraIntegrationFile` string fields to
  `runCommand`.
- Wire flags:
  - `--network` (string, default `""`, accepted: `""`, `"zrok"`,
    `"agora"`).
  - `--agora-integration-file` (string, default `""`).
- After `LoadConfig` and before `gateway.New`:
  - If `rc.network == "agora"`: ensure `cfg.Agora != nil`, set
    `cfg.Agora.Enabled = true`.
  - Resolve integration-file path: flag > env var > config field.
  - Write resolved path back to `cfg.Agora.IntegrationFile`.

**Acceptance.**

- `llm-gateway run config.yaml --network=agora` enables agora mode.
- `llm-gateway run config.yaml --network=foo` exits with a clear
  parse-time error.
- CLI flag > env var > config-field precedence verified for
  integration-file path.

**Depends on.** W9.

---

### W11 — Example config update

**Goal.** `etc/config.yaml` documents the new `agora:` block.

**Work.**

Add a fully commented-out `agora:` block to `etc/config.yaml` after
the existing `zrok:` block, matching the comment-density of the
existing semantic-routing block. Cover every field, every nested
block. Include commented `agora_service:` on each provider.

Do not add anything to `etc/dev.yaml`.

**Acceptance.**

- `etc/config.yaml` parses cleanly via `LoadConfig` (the block is
  commented out).
- Every field documented in the spec is mentioned with a brief
  description.

**Depends on.** W2.

---

### W12 — Unit tests

**Goal.** Test coverage matching the existing gateway's style.

**Work.**

Add test files alongside the implementation files:

- `gateway/agora_test.go` — subsystem construction, lifecycle
  ordering, failure-mode behavior.
- `gateway/agora_integration_test.go` — file loader, merge.
- `gateway/agora_capabilities_test.go` — derivation table.
- `gateway/agora_identity_test.go` — defaults, validation.

Tests use a fake `*agent.Agent` (or a small interface over the
catalog + tunnel calls). Network-level integration is W14.

**Acceptance.**

- `go test ./gateway/...` passes.
- New tests run in under 1 second total (no network, no real
  runtime).

**Depends on.** W3, W4, W5, W6, W7, W8.

---

### W13 — Provider integration tests

**Goal.** Verify each provider type (OpenAI, Anthropic, Local
single, Local multi-endpoint) correctly uses an agora_service
when configured.

**Work.**

Per-provider tests that:

1. Construct a `Config` with the provider configured with
   `agora_service: "test-service"`.
2. Stand up a stub HTTP server on a real local port.
3. Use a fake `agoraSubsystem` whose `ConnectAddress` returns the
   stub server's address.
4. Initialize the provider with that subsystem.
5. Issue a request through the provider's HTTP client.
6. Verify the request hit the stub server (not the provider's
   default URL).

For multi-endpoint Local, verify round-robin still works when
endpoints mix direct and agora_service.

**Acceptance.**

- Each provider type has at least one passing test.
- Multi-endpoint Local with mixed transports passes round-robin
  smoke.

**Depends on.** W8.

---

### W14 — Integration smoke

**Goal.** Manual or scripted end-to-end check against a real Agora
controller and a real enrolled environment.

**Work.**

Provision via Agora's standalone CLI:

```bash
agora controller start --addr 127.0.0.1:8080 &
agora admin org create gateway-services-org
agora admin account create --org gateway-services-org llm-gateway
agora admin workgroup create --org gateway-services-org gateway-services
agora admin contract create --org gateway-services-org gateway-services-default
agora enable <account-token>
```

Capture the workgroup ID, contract ID, env root.

Write gateway config covering all three layers:

```yaml
listen: 127.0.0.1:8080

providers:
  open_ai:
    api_key: "sk-test-not-real"

agora:
  enabled: true
  api_endpoint: http://127.0.0.1:8080
  instance_name: smoke-test
  description: "smoke test gateway"
  tunnel_mode: tcp

  advertisement:
    publish: true
    workgroup_ids:
      - <captured wg ID>
    contract_id: <captured con ID>

  serve:
    enabled: true
    grants:
      - smoke@example.com
```

Run `llm-gateway run config.yaml`. Verify:

- `advertisement published: adv_...` in logs
- `serve started: serve_id=svc_...` in logs
- `agora ad list` shows the advertisement
- `agora tunnel list` shows the serve
- HTTP server listening on 127.0.0.1:8080
- A second agora-enrolled host running `agora tunnel connect
  --name smoke-test --listen 127.0.0.1:9999` can dial the
  gateway through the fabric
- SIGINT retracts/removes everything within the shutdown budget

Bonus: add `providers.open_ai.agora_service: "openai-relay"` (with
a corresponding agora-side serve) and verify the gateway routes
OpenAI calls through it.

**Notes.**

- This is manual verification, not automated.
- Package the check as `bin/agora-smoke.sh` for repeat use.

**Acceptance.**

- Advertisement observed in catalog; serve observed in tunnel
  list; cross-host dial succeeds; clean shutdown.

**Depends on.** W10.

---

### W15 — Publish docs and update README

**Goal.** The spec moves from `docs/future/` to `docs/`, and the
project's user-facing docs acknowledge agora support.

**Work.**

- Move `docs/future/agora.md` → `docs/agora.md`. Edit opening
  paragraph: drop "proposed" framing.
- Move `docs/future/agora-work-order.md` to project archive (or
  delete; git preserves either way).
- In `README.md`, add an "Agora integration" subsection mirroring
  the zrok callouts. Two paragraphs ending with link to
  `docs/agora.md`.
- Update `docs/configuration.md`:
  - Add `agora:` to the top-level keys block.
  - Add `--network` and `--agora-integration-file` to the flags
    table.
  - Add a brief "Agora integration" subsection cross-linking
    `docs/agora.md`.
- Update `CHANGELOG.md` with a new entry covering all three layers.
- **Verify**: any `replace github.com/openziti/agora => ...`
  directive in `go.mod` is removed (acceptance check from W1).

**Acceptance.**

- `docs/future/agora.md` and `docs/future/agora-work-order.md` no
  longer in the live docs tree.
- `docs/agora.md` is the live reference, no "proposed" language.
- README acknowledges agora.
- `docs/configuration.md` documents agora.
- `CHANGELOG.md` has an entry.
- `go.mod` has no `replace` directive pointing at a local agora
  path.

**Depends on.** W14.

---

## Summary checklist

| Unit | File(s)                                        | Depends on |
| ---- | ---------------------------------------------- | ---------- |
| W1   | `go.mod`, `go.sum`                             | —          |
| W2   | `gateway/config.go`                            | W1         |
| W3   | `gateway/agora_integration.go`                 | W2         |
| W4   | `gateway/agora_capabilities.go`                | W2         |
| W5   | `gateway/agora_identity.go`                    | W2         |
| W6   | `gateway/agora.go`                             | W2, W3, W4, W5 |
| W7   | `gateway/agora.go` (serve methods)             | W6         |
| W8   | `gateway/agora.go` (connect methods), `gateway/providers/*` | W6 |
| W9   | `gateway/gateway.go`                           | W7, W8     |
| W10  | `cmd/llm-gateway/run.go`                       | W9         |
| W11  | `etc/config.yaml`                              | W2         |
| W12  | `gateway/agora_*_test.go`                      | W3..W8     |
| W13  | `gateway/providers/*_agora_test.go`            | W8         |
| W14  | `bin/agora-smoke.sh` (optional)                | W10        |
| W15  | `docs/agora.md`, `README.md`, `CHANGELOG.md`, `docs/configuration.md` | W14 |

## Risks and unknowns

- **`dd` snake-case mapping for acronym fields.** `APIEndpoint`,
  `WorkgroupIDs`, `AgoraService`, etc. Spike before W2; add
  explicit tags only where needed.

- **Connect port pre-allocation race.** The agora tunnel SDK
  doesn't echo back a kernel-assigned port (see the SDK spec's
  "Kernel-assigned connect ports" note), so the gateway pre-allocates
  by opening a `127.0.0.1:0` listener, capturing the resolved port,
  closing, and passing the concrete address to EnsureConnected.
  There is a microsecond race window between close and re-bind in
  which another process on the host could grab the port; acceptable
  for typical use because the gateway controls its own loopback.
  When agora adds `ResolvedListenAddress`, the pre-allocation step
  in W8 collapses to a plain `127.0.0.1:0` in the spec and the race
  disappears.

- **Provider init dependency on subsystem startup.** W9 wires the
  ordering carefully. If provider init has dependencies that need
  to happen before the agora subsystem (e.g., reading auth from
  somewhere), the ordering needs revisiting. Verify by tracing
  through `initProviders` against W9 acceptance.

- **Multi-endpoint Local with mixed transports.** Round-robin needs
  to treat agora_service-fronted endpoints identically to direct
  ones. The existing health-check loop is the riskiest piece — if
  it relies on probing the `base_url`, an agora-fronted endpoint
  will probe its local listen, which forwards across the fabric.
  That should work, but verify in W13.

- **Retract timing under shutdown pressure.** Same risk as the
  previous iteration: the shutdown context may already be cancelled
  by the time retract/remove runs. Use fresh bounded contexts (5s)
  for each cleanup operation inside the subsystem's shutdown path.

- **Standalone-Agent prerequisite slip.** If the agora SDK's
  `NewStandalone` lands later than expected, the gateway can
  proceed with a workaround: build `*Agent` via a small SDK-side
  helper (in the gateway's own SDK fork via the replace directive),
  documented as a temporary hack to be removed when `NewStandalone`
  lands. Track this in the W1 acceptance check.

- **`agora-serve` capability tag wording.** Whether
  consumers actually want a "this serves over Agora" tag in the
  catalog is a design question for Agora itself, not the gateway.
  If Agora has a stronger opinion on capability vocabulary, the
  derivation rule (W4) may need adjustment. Worth confirming with
  Agora's team before W15 docs publication.

- **Local replace directive hygiene.** W15 verifies removal.
  Critical: if the gateway tags a release with a replace pointing
  at a local path, downstream consumers' builds break.
