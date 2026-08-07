# Dynamic Key Management

Virtual API keys live in the gateway's config file today, which means the set of valid keys is fixed at process start. Minting a key, revoking one, or adjusting what a key may reach all require editing the config and restarting the gateway. For a gateway that fronts an organization's inference — where keys belong to agents and people who come and go on a different clock than the gateway's deployments — that coupling is the wrong shape.

This spec grows llm-gateway an extension point for reading keys from an external source, and a reload path that picks up changes in a running process. It is deliberately the *read* side only.

## The Organizing Idea

llm-gateway does not become a key management system. It becomes the consumer of a contract it publishes.

On one side of that contract is a management plane — a human with an editor, a provisioning script, or a full control plane with its own UI and audit log. It owns key lifecycle: minting, showing a secret to a human exactly once, revoking, recording who did what. On the other side is the gateway, which answers one question on every request — is this bearer token valid, and what may it reach — from a resident set of key records it periodically re-reads.

Everything that follows falls out of putting the boundary there. The gateway never needs an admin API, never needs its own auth boundary for privileged operations, and never needs to know how a key came to exist. A management plane can be built, replaced, or absent entirely; the gateway's behavior is defined by the records it reads, not by who wrote them.

## The Load-Bearing Property

**The data plane must not acquire a runtime dependency on the management plane.**

This is the property the whole design is arranged to protect, and it decides the extension point's shape before any other consideration gets a vote.

A key source that answers per-request — hand it a token, it tells you whether that token is good — is the obvious design and it is the wrong one. It makes every key source a single point of failure for all gateway traffic. A Postgres failover, a slow query, an unreachable management API, and the gateway starts returning 401 to clients whose credentials are perfectly valid. The blast radius of a management-plane problem becomes the entire data plane.

So sources hand over their whole set and the gateway holds it resident. A source that cannot be reached leaves the previous set in place. The management plane can be down for a week and the gateway keeps authenticating correctly — it simply stops learning about new and revoked keys, which is a bounded, observable, alarmable degradation rather than an outage.

The cost is a ceiling: every key is resident in memory and every refresh is a full read. For a key population measured in agents and people within an organization, that ceiling is nowhere near binding.

## The Shape

Three pieces.

**A source** is anything that can produce the complete current set of key records. Producing that set is the only thing a source is required to do — it is the entire surface a third party has to implement, and keeping it that small is deliberate. A source may *additionally* know how to signal that its data changed, and sources that have something native to offer here should use it, but that capability is optional and nothing depends on it.

```
// shape, not implementation
Load(ctx) (snapshot, error)              // required
Watch(ctx, notify func()) error          // optional
```

**The store** owns composition and residency. It holds the current snapshot, refreshes it, swaps it atomically, and is the only thing in the gateway that reads a source or touches the lookup map. Today's `KeyStore` grows into this role; the bearer-token middleware and the model/route checks keep working against whatever snapshot is current.

**A poll floor** runs underneath every source regardless of what signalling it offers. Push mechanisms are for latency and they all fail quietly: fsnotify delivers nothing on a network filesystem, a Postgres `NOTIFY` can be dropped, a webhook can be missed. Polling is what makes the gateway *converge* — a missed signal costs an interval, not an indefinite stale set. Push for latency, poll for convergence.

### Composition and Precedence

Multiple sources are live simultaneously. The config file's `keys` are implicitly the first source; declared sources follow in the order written. The intended shape is a break-glass key or two in the config alongside a fleet managed externally.

Collisions on key material resolve by that order — the earliest source wins — and the loser is logged with both source names and both entry names, never the token value. Precedence follows trust: a compromised management plane cannot shadow the operator's break-glass key. Within a single source a collision is unresolvable and stays what it is today, a directed startup error.

Config keys are read once at boot and never reload. They are a fixture of the process lifetime, which is the correct property for a break-glass credential and a documented limitation for anything else: a config key cannot be revoked without a restart. Partially re-reading the config file to make just this one section live is deliberately not done — it would leave the config with a contract where one section is live and every other section is not. Hand-authored keys that need to be live-editable are what the file source is for; point it at a second file.

### Name Collisions

`name` is a human label and the gateway's attribution identity — it is the `key` label on the request metrics and the identifier in the routing decision lines. Two records sharing a name do not collide on key material and both authenticate normally, but they report as one client in metrics, which matters directly for per-key-metrics and for gateway-side spend limits enforced against that label.

Duplicate names across the union are warned, not rejected. A name is expected to be unique and the published contract should say so, but a rotation window — a new key minted for the same principal while the old one is still valid — legitimately produces two records with one name. The warning is informational, and an operator seeing it mid-rotation is seeing the expected thing.

## The Key Record

The record is the actual artifact here. The Go interface is plumbing; the record is what a management plane implementer builds against, and it has to survive being expressed as a YAML document, as SQL rows, and as a JSON payload without distortion.

| field | meaning | required |
|---|---|---|
| `name` | human label and attribution identity | yes |
| `key` | plaintext key material | one of `key` / `key_sha256` |
| `key_sha256` | SHA-256 of the key material, hex | one of `key` / `key_sha256` |
| `allowed_models` | glob patterns; empty or absent means all | no |
| `allowed_routes` | semantic route names; empty or absent means all | no |
| `expires_at` | RFC3339 timestamp past which the key is invalid | no |

A snapshot carries a **version** alongside its records. It costs nothing now and it is the only thing that makes the record evolvable later — including the delta protocol deliberately left out of v1.

**Key material is hashed on the way in, whatever form it arrived as.** The table above is the *contract* — what a management plane writes — and it offers both forms. The domain record carries only a hash: each source's mapping layer hashes a plaintext `key` and passes a `key_sha256` through, so by the time a record reaches the store there is one field and no discriminator. Lookup hashes the incoming bearer token once before consulting the map. SHA-256 is the right primitive precisely because these are high-entropy minted tokens rather than passwords — a slow KDF would buy nothing and cost the auth path. Plaintext stays permitted where a human is the author (the config file, the YAML source); a management plane writing to a database or serving an API should store only the hash, so that the store on that side holds no recoverable secret. This is most of what the `key-storage` card wants, arriving as a side effect.

**`expires_at` is defined here but enforced by the `api-key-expiry` card.** Defining the field now costs a line in the schema; retrofitting it into a published contract costs a version bump. The two mechanisms are deliberately distinct: revocation is imperative and immediate, enacted by the management plane deleting the record; expiry is declarative and future-dated, and has to be a field because the gateway is the only party that can evaluate it at the moment of use. Expressing "stops working at midnight" as a scheduled deletion makes a security property depend on the management plane's punctuality.

**Revocation is deletion.** There is no `revoked_at` and no tombstone. The snapshot is the set of valid keys; a record that is not in it is not valid. This means the gateway cannot answer "who had access last Tuesday," and should not — key lifecycle history belongs to the management plane, on the other side of the contract.

### Three Encodings

The file and HTTP encodings both ship. The SQL sketch is here to show the record survives a relational shape; it is not a published contract.

```yaml
# keys.yaml — the file source
version: 1
keys:
  - name: alice
    key: "sk-gw-abc123..."
    allowed_models: ["claude-*", "gpt-*"]
    allowed_routes: ["coding", "general"]
  - name: ci-pipeline
    key_sha256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
    expires_at: "2026-12-31T23:59:59Z"
```

```sql
-- intent sketch, not a published contract
CREATE TABLE api_keys (
  key_sha256     TEXT PRIMARY KEY,
  name           TEXT NOT NULL,
  allowed_models TEXT[] NOT NULL DEFAULT '{}',
  allowed_routes TEXT[] NOT NULL DEFAULT '{}',
  expires_at     TIMESTAMPTZ
);
CREATE INDEX api_keys_name_idx ON api_keys (name);
```

`key_sha256` is the primary key rather than `name`, so a rotation window can hold two valid records for one principal. Postgres arrays keep the snapshot a single-table read; child tables are the normal-form alternative and cost a join on every refresh.

```json
{
  "version": 1,
  "count": 2,
  "keys": [
    { "name": "alice", "key_sha256": "...", "allowed_models": ["claude-*"], "expires_at": null }
  ]
}
```

## The HTTP Source

The HTTP source is the one that makes the extension point pay for itself. A management plane keeping its keys in Postgres, MySQL, SQLite, Vault, or anything else adapts on its own side of the contract and the gateway never learns a second storage technology. One source implementation covers every backend, which is the opposite of the accretion a per-database source would start.

It is also nearly free here. `net/http` is already in the tree, and the overlay transports are already wired with boot-time validation — a key API reachable only through an agora tunnel, never publicly exposed, is this repo's existing idiom applied at a new site rather than a new mechanism.

### The Contract

`GET {base_url}/v1/keys` returns the complete current key set in the JSON shape above.

Two versions are in play and they mean different things. The `/v1/` in the path is the **API** version — the endpoint and envelope shape — and lets a management plane serve two versions side by side during a migration. The `version` field in the body is the **record schema** version, and it appears in every encoding including the YAML file, which has no path to carry it. Keeping the record version in the document is what lets the record stay transport-free.

`count` exists so truncation is loud. A v1 gateway that received a silently paginated response would apply a partial key set, and a missing key is a failed authentication for a client whose credentials are fine. The gateway compares `count` against the records it received and fails the refresh on a mismatch, so a server that starts paginating breaks visibly instead of quietly denying people.

### Freshness

The response carries an `ETag`; the gateway sends `If-None-Match` on every poll. A `304` costs a round trip instead of a payload, which is what makes polling cheap enough to be the primary mechanism rather than a fallback.

A `304` is a **successful** refresh. It confirms the snapshot is current, so it resets the staleness clock exactly as a `200` does. Treating it as a no-op would make a stable key set look progressively more stale and eventually trip `max_staleness` on a source that is perfectly healthy.

### Reaching the Endpoint

Two mechanisms, composable, both optional.

A bearer token (`token: "${KEYS_API_TOKEN}"`) is sent as `Authorization: Bearer`. And the source accepts `agora_tunnel` or `zrok_share_token` the way a provider does, so the key API can live entirely inside the overlay — where network reachability is itself the authentication and a bearer token may be redundant.

That reuse has a concrete integration point worth naming: `collectAgoraTunnels` currently mirrors `initProviders`' gates exactly so the dialer attaches precisely the tunnels that get wired, and the repo's own journal flags that the two must change together. A key source that dials a tunnel has to be collected there too, or its tunnel is never attached.

### Failure Modes

A `200` with a body that parses and whose `count` agrees becomes the new snapshot. A `304` confirms the current one. Everything else is a refresh failure that logs and holds last-known-good: a 5xx, a timeout, a body that will not parse, a `count` mismatch, or a `version` the gateway does not understand.

A `401` or `403` is called out separately in the log. It means the gateway's own credential to the key API is wrong or expired, which is an operator problem that will not resolve on its own, and it should not read like a transient upstream blip.

Every refresh carries a timeout, so an endpoint that accepts a connection and then hangs costs one interval rather than wedging the refresh loop.

## Reload Semantics

**Boot fails loud. Reload does not.** This is the same split the repo already enforces elsewhere: a config the gateway cannot honor is a directed startup error, but a gateway that is already serving must not be taken down by a typo in a key file. A refresh that fails — unreadable file, malformed YAML, unreachable endpoint, schema version the gateway does not understand — logs at every attempt and leaves the previous snapshot in place.

A refresh is atomic and all-or-nothing per source. A partially-parsed file never produces a partially-applied key set.

**At boot there is no last-known-good, so a source that cannot load is fatal by default.** Starting successfully with a key set the gateway knows is wrong means starting into a state where every affected client gets a 401 from a gateway that reports itself healthy — far harder to diagnose than a process that refuses to start with a directed error naming the source it could not reach. The cost is real and worth stating: if the key API is unreachable at the moment a gateway restarts, that gateway does not come up.

A per-source `required: false` is the opt-out, for an operator who would rather come up degraded — config break-glass keys only — than not come up at all. It is the same shape of knob as `max_staleness`: a policy choice about which failure is preferable, with the default set to the one that is louder rather than the one that is quieter.

**Staleness is observable.** Because revocation is deletion and a failed refresh holds last-known-good, a revocation that never lands is a key that stays valid indefinitely: the operator deleted the record, believes the key is dead, and the gateway is serving from a snapshot taken before the deletion. Last-known-good is right for availability, but with respect to revocation it fails open, and that is not something to leave implicit. Each source carries the age of its last successful load, exposed as a gauge and as a warning that escalates as the age grows, so the condition is alarmable rather than silent.

**`max_staleness` is the opt-in for operators who would rather fail closed.** Past the configured age, a source's contribution is dropped from the union — not the entire snapshot, and not the gateway. A stale Postgres source stops contributing its keys while the config's break-glass key, which is boot-resident and never stale, keeps working. The default is unbounded: making fail-closed the default would trade a gateway-wide outage for a revocation guarantee, and for a gateway that is the wrong way round.

**An empty snapshot is a legitimate intent.** Since revocation is deletion, "revoke everything" is deleting every record, so an empty result is accepted rather than treated as a suspected truncation. Zero keys means deny-all, which is fail-closed and therefore safe; it is logged loudly enough that an accidentally truncated file is obvious within seconds. This also resolves the existing boot check — `enabled: true` with no keys was fatal because it used to mean *open* access, and once the store treats zero keys as deny-all that check becomes a usability guard rather than a safety one.

**The snapshot is bound at authentication time.** A request that authenticates against snapshot N completes against it, holding the record it matched, even if snapshot N+1 revokes the key mid-flight. For a normal request this is invisible. For a long-running streaming completion it means a revoked key can continue producing tokens for the life of that stream. This is a decision rather than an oversight — cancelling in-flight work on revocation means threading revocation checks through the streaming path — and it is worth revisiting if streams get long enough that the window becomes uncomfortable.

## Triggers

One reload path, several ways to provoke it.

**`SIGHUP`** is the deterministic path and always available. It is dependency-free, trivially testable, works regardless of how the file was edited, and is the traditional daemon idiom. The gateway already runs a signal loop for `SIGINT`/`SIGTERM`; `SIGHUP` joins it. Windows has no `SIGHUP`, and the project builds Windows binaries, so the signal registration needs to be platform-guarded.

**fsnotify** watches the file source. It is convenient and genuinely fiddly, and the implementation has to account for it: editors replace files by rename, so the watch goes on the containing directory rather than the file; Kubernetes ConfigMap mounts swap a symlink and produce a different event shape again; a writer streaming the file produces partial-write events that need debouncing. It fires the same reload path `SIGHUP` does.

**Polling** runs under every source at a configured interval. For the HTTP source it is the primary mechanism rather than a floor, because conditional requests make it cheap: a poll against an unchanged key set is a `304` and a round trip.

**Native source signals** are used where a source has one — a future Postgres source could `LISTEN` on a channel the management plane `NOTIFY`s after a write, which is the direct analog of fsnotify. Nothing depends on these; they lower latency below the poll interval.

## Configuration Surface

```yaml
api_keys:
  enabled: true

  # first source, implicitly. read once at boot; never reloads.
  keys:
    - name: breakglass
      key: "${BREAKGLASS_KEY}"

  # additional sources, in precedence order after the config keys.
  sources:
    - type: file
      path: /etc/llm-gateway/keys.yaml
      watch: true
      poll_interval: 30s

    - type: http
      base_url: "https://keys.internal"   # or reached over the overlay, below
      token: "${KEYS_API_TOKEN}"
      agora_tunnel: keys
      poll_interval: 30s
      timeout: 5s
      required: true                      # default

  reload:
    max_staleness: 0    # 0 = unbounded (default)
```

`${VAR}` expansion continues to run once at config load, covering the config's own keys as it does today and now the HTTP source's `token` and `base_url` alongside them — same owner, same rule, a written-non-empty value that resolves empty is a boot error. A source's *contents* are never expanded: a key file or an API payload written by a management plane is data, not configuration, and `${...}` inside it is a literal.

## Scenarios

**Revoking a key.** An operator deletes alice's record from `keys.yaml`. fsnotify fires, the file source reloads, the store swaps the snapshot, and alice's next request gets a 401 from the auth middleware. A streaming completion alice already had open continues to completion. Total elapsed time from save to effect is a debounce interval.

**The management plane goes dark.** The key API becomes unreachable at 02:00. The gateway logs a refresh failure on every attempt and continues authenticating from the snapshot it loaded at 01:59. The staleness gauge climbs; a warning escalates with it. Every existing key keeps working and no client notices. At 09:00 the API returns, the next poll succeeds, and the snapshot catches up. If the operator had configured `max_staleness: 1h`, the HTTP source would have dropped out of the union at 03:00 and only the config's break-glass key would still authenticate.

**A restart during that outage.** Same 02:00 outage, but the gateway is rescheduled at 04:00. There is no snapshot to hold, so the HTTP source fails at boot and the process exits with a directed error naming the endpoint it could not reach — visibly broken rather than up and denying everyone. An operator who has decided that a degraded gateway beats an absent one sets `required: false` on the source and comes up with the break-glass key alone.

**A collision with the break-glass key.** A management plane bug re-mints a key whose value matches the config's `breakglass` entry. The union resolves in config's favor, a warning names both sources and both entry names, and requests bearing that token authenticate as `breakglass` with the config's permissions. The token itself never appears in a log line.

## Seam Census

Four boundaries are live in this design.

**data plane / management plane — separate.** Sources produce whole sets and the gateway holds them resident; no key source is ever consulted inside a request. *Why:* a per-request source makes management-plane availability a precondition for all gateway traffic. *Revisit if:* the key population outgrows resident memory, or a source appears that genuinely cannot enumerate its keys — a token-introspection endpoint, for instance — at which point the boundary has to be redrawn rather than stretched.

**key record / encodings — separate.** The domain record carries no YAML, SQL, or JSON knowledge; each source maps its own wire shape into it. *Why:* the repo's existing practice fuses these — `APIKeyEntry` *is* the config struct, `dd`-marshaled in place — and while the file source was the only encoding, that fusion was the cheaper call. A second encoding shipping alongside it makes the boundary one that more than one party meets at, which is where it earns its cost. Key material normalization lands in the mapping layer as a consequence: wire shapes carry either `key` or `key_sha256`, and the domain record carries only a hash. *Revisit if:* the second encoding turns out not to ship after all, at which point the mapping layer is ceremony over a single format.

**store as sole owner of the snapshot — enforce.** The store is the only thing that reads a source, holds the snapshot, or performs key lookup. Nothing reaches around it to consult a source directly or to hold a reference to the map. *Why:* recorded so review can catch a bypass, rather than reconstructing the intent from a diff.

**error by tier — boot fail-fast, reload log-and-continue.** A config or source the gateway cannot honor at startup is a directed error and the process does not start, `required: false` being the explicit opt-out. A refresh failure in a running gateway logs on every attempt and holds the previous snapshot. The fail-closed policy on staleness is opt-in with an unbounded default. *Why:* the standing split in this repo, applied to a new long-running loop. The two defaults point in opposite directions on purpose — loud at boot where there is no good state to fall back to, quiet in flight where there is — and both are the choice that makes a broken deployment visible rather than silently degraded.

## Deferred (and Why)

**A management/admin API.** The card's original discussion framed the unknown as the admin surface's own auth boundary — who may manage keys and how they authenticate, distinct from the data-plane keys being managed. That question is real and it is not answered here, because this spec removes the need to answer it: an external source plus reload delivers the operational value — revoke without a restart, keys living outside the config that also holds provider credentials, keys managed by whatever already manages secrets in the deployment — with no new privileged surface. If a write side is wanted later, this read side is the substrate it would write through, so nothing here is wasted.

**A Postgres source.** The HTTP source covers a Postgres-backed management plane already, with the adaptation on the management plane's side where it belongs. A direct database source only earns its place if a deployment genuinely cannot stand up an endpoint, and it would bring a driver dependency and a Postgres-specific contract into a gateway that has neither today. The DDL sketch above stays a sketch until then.

**A reference adapter that serves the contract from a database.** A small standalone binary — the way `cmd/dummy-model` already is — turning "we have a table" into "run this." It would make the DDL real and exercised without putting a database driver in the gateway. Attractive, not decided, and a genuine extra deliverable rather than a detail of this one.

**A persistent snapshot cache.** Writing the last good snapshot to disk would let a restart during a management-plane outage come up with last-known-good instead of failing, which is the sharpest edge in the boot story. It also introduces a second place keys live on disk, with its own staleness and its own security posture, so it wants deciding on purpose rather than as a convenience.

**Pagination.** v1 fetches the whole set in one response, and `count` makes a truncating server fail loudly rather than quietly. If key populations ever reach the size where that matters, pagination is a v2 API version, not a silent addition.

**Incremental/delta refresh.** v1 is full-fetch. Deletions cannot be expressed in a watermark-based delta feed without tombstones, and tombstones are exactly what "revocation is deletion" removes. The version field is what keeps this reachable: a delta feed would be a v2 record schema that reintroduces them, and the gateway would know which it is talking to.

**Expiry enforcement.** The field is defined here so the record is complete; evaluating it at request time is the `api-key-expiry` card.

**Hashed-only storage.** The store hashes everything on the way in, which is the mechanism `key-storage` needs. Removing plaintext from the config file entirely is that card's call, not this one's.

**Per-source reload policy overrides.** `max_staleness` is global in v1. Sources with genuinely different freshness requirements would want their own, and the config shape leaves room.

**Cancelling in-flight work on revocation.** A streaming completion outlives the revocation of the key that started it. Worth revisiting if stream durations grow.
