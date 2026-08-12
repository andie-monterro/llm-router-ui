# Key Source Follow-ons

Dynamic key management's read side is complete: llm-gateway consumes complete resident key sets from config, YAML files, and an HTTP API; refreshes them without coupling authentication requests to the management plane; and publishes the v1 record and transport contracts in `docs/current/key-sources.md`. The remaining ideas are independent follow-ons rather than unfinished parts of that implementation.

## Management and Write Surfaces

### Management API

A create, revoke, or list API still needs its own privileged authentication and authorization boundary, distinct from the data-plane keys it manages. The gateway does not need that surface to support runtime changes: an external management plane can write a file or serve the HTTP source contract already. If a write side is added later, it should write through a management-owned store and let the existing read side consume it rather than mutating the resident snapshot directly.

### Reference Database Adapter

A small standalone adapter could serve the HTTP contract from Postgres, SQLite, or another database, turning “we have a table” into a runnable reference deployment without adding database drivers to llm-gateway. A direct database source belongs in the gateway only if a real deployment cannot expose an HTTP endpoint and the operational benefit justifies a backend-specific dependency.

## Availability

### Persistent Last-Known-Good Snapshot

Today a required source must load successfully after every restart. Persisting the last accepted snapshot would let a gateway restart during a management-plane outage, but creates another place key material lives on disk and needs explicit integrity, confidentiality, age, and invalidation rules. This should be designed as a security-sensitive cache rather than added as incidental startup convenience.

### Per-Source Reload Policy

`max_staleness` is global in v1. Deployments with sources carrying different trust or revocation requirements may need per-source limits, warning thresholds, or availability policies. Any override should preserve the validation rule that a nonzero limit exceeds that source's poll interval plus timeout.

## Protocol Evolution

### Pagination

The v1 HTTP API returns the complete set in one response and rejects pagination indicators. Larger populations would require a versioned API whose snapshot boundary is explicit across pages; silently adding pagination to v1 would make partial sets indistinguishable from authoritative ones.

### Incremental Refresh

The v1 record schema is full-fetch. A delta protocol needs ordering, replay, a complete-snapshot checkpoint, and tombstones for deletion. Those requirements conflict with the simple “revocation is absence” v1 model, so incremental delivery belongs in a new schema/API version rather than as optional v1 fields.

## Request Lifetime

Authentication binds a record to a request. A streaming completion that began before revocation is allowed to finish even if a later snapshot removes the key. Cancelling in-flight work on revocation would require tracking live requests by credential identity and deciding whether expiry, source exclusion, and ordinary deletion all have the same cancellation semantics.

## Existing Roadmap Cards

The remaining expiry ergonomics live in `docs/future/roadmap/api-key-expiry.md`. The remaining plaintext-free inline-storage decision lives in `docs/future/roadmap/key-storage.md`. Per-key metrics, spend limits, rate limiting, and model-list filtering retain their own roadmap cards.
