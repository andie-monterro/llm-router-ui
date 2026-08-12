# Dummy Keys

`dummy-keys` is a reference key API. It serves the gateway's published `GET /v1/keys` contract from a local file, so an HTTP key source can be exercised without standing up a management plane.

It exists for two reasons. Every other part of dynamic key management can be tried with a text editor; the HTTP source cannot, because it needs something on the other end. And the design's load-bearing behaviours are failure behaviours — last-known-good, staleness, exclusion, the unsolicited-`304` guard — none of which are observable against a server that always behaves. `--fault` makes them observable on demand.

## Running

```bash
dummy-keys keys.json
# dummy-keys listening on ':8082' serving 'keys.json' (fault=none)
```

The key file is either a full v1 envelope or a bare list of records, so the same document can be handed to a file source or to this server:

```json
{
  "version": 1,
  "keys": [
    {"name": "alice", "key": "sk-gw-alice", "allowed_models": ["dummy"]},
    {"name": "ci", "key_sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"}
  ]
}
```

The file is re-read on every request. Editing it and watching the gateway converge is the demo; the `ETag` changes with the contents, so a conditional poll starts returning `200` again by itself.

## Flags

| Flag | Default | Description |
|---|---|---|
| `--listen` | `:8082` | listen address |
| `--token` | (none) | require this bearer token; empty accepts any request |
| `--fault` | `none` | failure to inject (below) |
| `--fault-status` | `500` | status returned by `--fault status` |
| `--stall-for` | `1m` | how long `--fault stall` holds the response open |

An unreadable key file is a startup error rather than a first-request failure.

## Faults

Each fault provokes a specific gateway behaviour that is otherwise hard to reach.

| `--fault` | What the server does | What the gateway should do |
|---|---|---|
| `status` | replies with `--fault-status` | refresh failure; holds last-known-good |
| `count` | sends a `count` one higher than `keys` | rejects the refresh — the truncation guard |
| `pagination` | adds `Link: …; rel="next"` | rejects the response; v1 does not paginate |
| `unsolicited` | replies `304` even to an unconditional request | rejects it; there is no validator to confirm |
| `stall` | holds the response open | request timeout, then one interval lost |
| `malformed` | sends an envelope with no `keys` member | rejects the refresh |
| `unknownfield` | adds `allowed_model` to every record | rejects the refresh — strict decoding |

## Using it as a key source

```yaml
api_keys:
  enabled: true
  sources:
    - type: http
      name: control-plane
      base_url: "http://localhost:8082"
      poll_interval: 5s
      timeout: 2s
```

With that wired, the behaviours worth watching by hand:

- **Live revocation.** Delete a record from the key file. The next poll — or an immediate `SIGHUP` to the gateway — drops it, and that key's next request gets a 401.
- **The management plane going dark.** Stop `dummy-keys`. The gateway keeps authenticating from the snapshot it already holds and logs a refresh failure carrying the age of the last successful load. Existing keys keep working; the gateway simply stops learning about new and revoked ones.
- **Failing closed.** Set `reload.max_staleness` above `poll_interval + timeout`, then stop the server. Past that age the source's contribution leaves the union and only config keys still authenticate.

See [Key Sources](key-sources.md) for the record contract and the full source configuration.
