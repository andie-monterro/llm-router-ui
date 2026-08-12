# Key Sources

Key sources let llm-gateway refresh virtual API keys without restarting. A source hands the gateway its complete current set; the gateway composes all source contributions into one resident snapshot and never calls a management plane from the request path. If a refresh fails, requests continue against the last accepted snapshot unless the operator has configured a staleness limit.

The gateway ships three key encodings:

1. Inline records in the main gateway configuration, loaded once at boot.
2. A reloadable YAML file containing a versioned key document.
3. A reloadable HTTP JSON API at `GET {base_url}/v1/keys`.

## Configuration

Inline keys and declared sources share one ordered `api_keys` block:

```yaml
api_keys:
  enabled: true

  # first contribution, loaded once; useful for break-glass access
  keys:
    - name: breakglass
      key: "${BREAKGLASS_KEY}"

  # later contributions, in declaration order
  sources:
    - type: file
      name: operators
      path: "/etc/llm-gateway/keys.yaml"
      watch: true
      poll_interval: "30s"
      required: true

    - type: http
      name: management-plane
      base_url: "${KEY_API_BASE_URL}"
      token: "${KEY_API_TOKEN}"
      agora_tunnel: "key-api-egress"
      poll_interval: "30s"
      timeout: "5s"
      required: true

  reload:
    max_staleness: "10m"
```

Durations must be strings with units. A bare `30` is rejected rather than interpreted as nanoseconds. `max_staleness: 0` is the one numeric-zero exception and means unbounded, which is also the default.

Every source has a stable identity used in logs and metric attributes. `name` is optional and falls back to `<type>[<index>]`, such as `file[0]`; `config` is reserved for inline keys. Identities must be unique.

### File Source Fields

| Field | Default | Meaning |
|---|---|---|
| `type` | required | Must be `file` |
| `name` | positional | Stable source identity |
| `path` | required | YAML document path |
| `watch` | `false` | Watch the containing directory for file replacement and ConfigMap-style swaps |
| `poll_interval` | `30s` | Convergence floor even when watching |
| `required` | `true` | Whether a failed initial load prevents startup |

### HTTP Source Fields

| Field | Default | Meaning |
|---|---|---|
| `type` | required | Must be `http` |
| `name` | positional | Stable source identity |
| `base_url` | required | Absolute `http` or `https` URL; `/v1/keys` is appended |
| `token` | empty | Bearer credential sent to the key API |
| `agora_tunnel` | empty | Reach the API through an Agora dial tunnel |
| `zrok_share_token` | empty | Reach the API through a zrok access |
| `poll_interval` | `30s` | Refresh cadence |
| `timeout` | `5s` | Per-request deadline |
| `required` | `true` | Whether a failed initial load prevents startup |

`${VAR}` references in an HTTP source's `base_url` and `token` are resolved once at configuration load. Source documents are data and are never environment-expanded. When both overlay fields are configured, Agora wins and the choice is logged without exposing either credential.

## Record Contract

File and HTTP sources publish the same record:

| Field | Requirement | Meaning |
|---|---|---|
| `name` | required, non-empty | Stable attribution identity, compared exactly |
| `key` | exactly one of `key` or `key_sha256` | Plaintext bearer-token value |
| `key_sha256` | exactly one of `key` or `key_sha256` | SHA-256 digest as exactly 64 hexadecimal characters, case-insensitive |
| `allowed_models` | optional or null | Go `path.Match` globs; absent or empty permits all |
| `allowed_routes` | optional or null | Exact semantic-route names; absent or empty permits all |
| `expires_at` | optional or null | RFC3339 timestamp with an explicit offset; the key is invalid at or after this instant |

Exactly one key-material form is required. The digest is SHA-256 over the exact plaintext bytes with no trimming, case folding, or Unicode normalization. Plaintext must match the bearer-token `b64token` grammar documented in [Virtual API Keys](api-keys.md). A management plane should store and publish `key_sha256` so it holds no recoverable gateway credential.

`allowed_models` uses Go's `path.Match` rules. `/` is a separator and neither `*` nor `?` crosses it, so `*` does not match `meta-llama/Llama-3-70B`; use `*/*` or a more specific slash-aware pattern. `allowed_routes` values are literal, not globs.

Names are attribution identities rather than display labels. `Alice` and `alice` are different identities; renaming a record starts a new metric identity. Reusing a name during rotation deliberately groups both credentials under one principal.

All encodings are strict. Unknown or duplicate fields, null required values, invalid patterns, malformed hashes, and invalid timestamps reject the complete configuration or source refresh. Optional restriction and expiry fields may be null and are treated as absent.

## Inline Encoding

Inline records sit directly under `api_keys.keys` and accept `name`, plaintext `key`, `allowed_models`, and `allowed_routes`:

```yaml
api_keys:
  enabled: true
  keys:
    - name: breakglass
      key: "${BREAKGLASS_KEY}"
      allowed_models: ["gpt-*", "claude-*"]
```

Inline records have no document envelope, accept neither `key_sha256` nor a valued `expires_at`, and are loaded only at startup. Their key values support one-time `${VAR}` expansion.

## File Encoding

A file source reads one complete YAML document:

```yaml
version: 1
keys:
  - name: alice
    key: "sk-gw-alice-example"
    allowed_models: ["claude-*", "gpt-*"]
    allowed_routes: ["coding", "general"]

  - name: ci-pipeline
    key_sha256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
    expires_at: "2026-12-31T23:59:59Z"
```

`version` and `keys` are required, `version` must be `1`, and `keys: []` is the explicit empty set. The file encoding has no `count` field.

**Quote every YAML `expires_at` value.** The strict YAML intake currently rejects an unquoted timestamp scalar, including the form emitted for `time.Time` by plain `yaml.v3`. The error names the offending `keys[n].expires_at` path and tells the operator to quote it. JSON does not have this constraint.

Polling always runs. With `watch: true`, filesystem notifications provide lower latency, but they do not replace polling; the watch follows atomic replacement by monitoring the containing directory rather than one inode.

## HTTP Encoding

The v1 API is:

```http
GET {base_url}/v1/keys
```

```json
{
  "version": 1,
  "count": 1,
  "keys": [
    {
      "name": "alice",
      "key_sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
      "allowed_models": ["claude-*"],
      "expires_at": null
    }
  ]
}
```

`version`, `count`, and `keys` are required. `count` is the size of the complete authoritative key set before any response limiting and must equal the number of records delivered. The v1 API does not paginate: any `Content-Range` header or a `Link` relation of `next`, `prev`, `first`, or `last` rejects the response, including on a `304`.

Every request carries `Cache-Control: no-cache`. When `token` is configured it also carries `Authorization: Bearer <token>`. After an accepted response with an ETag, later polls send `If-None-Match`; a conditional `304` confirms the resident contribution and resets its freshness age. A `304` answering an unconditional request is a protocol failure.

An ETag commits only with a fully accepted `200`. A rejected body does not advance it, and an accepted `200` without an ETag clears the previous validator. A missing ETag is otherwise valid and simply causes unconditional polling.

`401` and `403` responses produce a credential-specific operator log. Any other non-`200`/conditional-`304`, timeout, oversized body, strict-decode error, version failure, count mismatch, or pagination indicator is a failed refresh. Response bodies are capped at 32 MiB and never included in diagnostics.

The gateway injects the HTTP client. Direct sources use the normal client; Agora and zrok sources borrow clients owned by those subsystems. The source applies `timeout` to each request context and never mutates the borrowed client.

## Composition and Precedence

Inline `config` keys are first; declared sources follow in written order. When two sources publish the same key value, the earliest source wins and a warning names both sources and records without naming the value. The usual layout is one or two inline break-glass keys followed by externally managed sources.

A source excluded for staleness does not promote a suppressed duplicate from a lower-precedence source: failing closed must not silently reassign the credential. Deleting the winning record is different and may promote an independently published duplicate from a later source, because deletion removes only the winning source's claim. Cross-source collision warnings should therefore be treated as configuration defects.

Duplicate record names do not affect authentication and produce a warning. They intentionally allow a rotation window, but two unrelated clients sharing a name merge their attribution.

An empty composed snapshot is valid and means deny all. It is logged prominently rather than reinterpreted as open access.

## Boot, Refresh, and Staleness

Initial loading is fatal by default because there is no last-known-good snapshot at boot. `required: false` lets the gateway start without that source's contribution, normally alongside an inline break-glass key. Invalid configuration and inability to construct a requested overlay remain startup errors; `required` is an availability policy, not a strictness override.

After startup, every failed refresh logs and retains the last accepted contribution. Refreshes are serialized per source, concurrent source I/O commits through one store transition, and bursts collapse to one follow-up load. On Unix, `SIGHUP` triggers all reloadable sources; Windows has no reload signal. Native watches remain latency hints and polling remains the convergence mechanism.

`reload.max_staleness` opts into fail-closed behavior. Once a source's last successful load is older than the configured duration, that source is excluded from the union on an independent exact-deadline timer. Its contribution remains retained so a later conditional `304` can reinstate it. A nonzero limit must be strictly greater than every source's `poll_interval + timeout` (`timeout` is zero for file sources), preventing a healthy source from flapping while its scheduled load is still allowed to run.

Expiry is separate from source staleness. `expires_at` is checked on each bearer-token lookup at the exact boundary, regardless of refresh cadence.

## Observability

Key-source diagnostics name source identities, record names, status codes, and structural paths, never plaintext keys, digests, response bodies, request headers, or overlay credentials. Refresh failures log on every attempt; warnings escalate to errors at ten poll intervals of age. Exclusion, reinstatement, cross-source collisions, duplicate identities, and a deny-all union are logged explicitly.

The key subsystem publishes four OpenTelemetry instruments:

| Instrument | Kind | Attributes |
|---|---|---|
| `llm_gateway.keys.source.staleness` | gauge, seconds | `source` |
| `llm_gateway.keys.source.excluded` | 0/1 gauge | `source` |
| `llm_gateway.keys.refresh` | counter | `source`, `result=success\|not_modified\|failure` |
| `llm_gateway.keys.resident` | gauge | none |

The staleness and exclusion gauges cover reloadable sources only. A source that has never loaded successfully emits neither series until its first success; the refresh-failure counter and logs carry that degraded state.
