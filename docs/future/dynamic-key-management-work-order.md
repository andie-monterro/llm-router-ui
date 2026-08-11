# Dynamic Key Management — Work Order

This translates [`dynamic-key-management.md`](dynamic-key-management.md) into concrete code against this repo. The spec converged through five mercurius rounds (`ready_to_build`, twenty-five findings, all fixed) and is settled; nothing here re-opens it. What this document adds is everything the spec deliberately left out: package layout, the decode pipeline, the composition algorithm, the wiring into `gateway.New` and `Run`, the config validation table, the staged landing plan, and the tests.

Where this work order goes beyond the spec — a body size cap, a strictly-greater staleness accommodation, an exact-deadline exclusion timer, and one place where it knowingly *narrows* the published contract — those decisions are collected under [Additions Beyond the Spec](#additions-beyond-the-spec) rather than smuggled into the body. The narrowing is the one worth reading before implementation starts, because it is the only item there that a third party can observe.

## Decisions Taken Before Drafting

Three scoping questions were resolved with the operator before this draft:

**The key subsystem becomes a new top-level `keys/` package.** It follows the `agora/` and `routing/` idiom — a subsystem package owning its own config types and its own `${VAR}` resolution — and it makes the spec's third seam mechanically enforced rather than review-enforced: with the lookup map and the source implementations unexported, a bypass does not compile.

**`api_keys.keys` in the main config adopts strict decoding and bearer-token grammar validation.** An unknown field under a config key becomes a boot error instead of silence, a coerced value — a timestamp-shaped `name` silently rewritten by the forgiving binder — becomes a boot error instead of a quietly altered attribution identity, and a key value outside the `b64token` grammar becomes a boot error instead of an unreachable credential. Delivered by a strict second-pass bind of the inline records, not by an unknown-field check alone; see [Config Source](#config-source). This is a deliberate, loud, breaking change for a config carrying any of those faults today.

**`docs/current/api-keys.md` gets its wildcard claim corrected, not its behavior.** The doc currently asserts `allowed_models: ["*"]` is unrestricted, which `path.Match` makes false for any model id containing a `/`. This work fixes the sentence; the behavior stays on the `wildcard-model-restrictions-deny-namespaced-models` card.

## Dependency Changes

**`github.com/michaelquigley/df` v1.0.1 → v1.0.2.** This is the load-bearing dependency change and it removes most of what would otherwise be hand-rolled decoder work. `v1.0.2` ships `dd.Strict()`, an acceptance mode built for "data whose exact spelling is the contract." Verified against every rule in the spec's strict-decoding section, with the full gateway suite green on the bump:

| spec rule | provided by `dd.Strict()` |
|---|---|
| unknown fields, envelope and nested record | yes — `Doc.Keys[0]: unknown field "allowed_model"` |
| duplicate keys, YAML | yes |
| duplicate keys, JSON | yes — `$: duplicate key "version"` |
| `version` / `keys` / `count` required | yes, via `+required` |
| null where a value is required | yes — `keys: null`, `count: null`, `name: null` all reject |
| no type coercion | yes — `version: "1"` and `name: 42` reject |
| `count` is an integer | yes — `number 3.7 is not an integer` |
| errors name structure, not values | mostly — two exceptions, both handled (see [Never Log a Secret](#never-log-a-secret)) |
| multi-document YAML, JSON trailing data, YAML anchors | yes — all rejected, beyond what the spec asked for |

**`github.com/fsnotify/fsnotify` v1.9.0, indirect → direct.** Already in the module graph; the file source's watch promotes it. No version change.

Four `dd` behaviors this work routes around are documented, with reproductions, in the `DF-REQUESTS.md` note handed to the df team. A `v1.0.3` addressing any of them simplifies the corresponding piece here; none of them block the work:

- **Null carve-out** — `dd.Strict()` rejects `allowed_models: null`, `allowed_routes: null`, and `expires_at: null`, which the spec requires read as *absent*. Handled by normalizing between intake and binding; see [The Decode Pipeline](#the-decode-pipeline).
- **`!!timestamp` at strict intake** — an unquoted YAML timestamp is rejected, and plain `yaml.v3` marshals `time.Time` unquoted. This becomes a stated rule on the published contract: **timestamps in the YAML encoding must be quoted.** It is the one item with no cheap gateway-side fix, because the rejection happens at intake before any normalization hook, and it is the one place this work knowingly narrows the contract — see [Additions Beyond the Spec](#additions-beyond-the-spec) for why that is acceptable here and what has to be true for it to stay acceptable. The file source re-words the resulting error so it names the fix.
- **`dd` renders values into errors and does not honor `+secret`** — at *both* the intake and binding stages. Handled by sanitizing every decode error whose structural path sits under a record; see [Never Log a Secret](#never-log-a-secret).
- **`dd.Dynamic` and `dd.Strict()` do not compose** — the strict binder rejects the `type` discriminator `dd` itself requires. Routed around rather than worked around: a struct carrying `+extra` opts out of unknown-field rejection while keeping every other strict rule, so `type` lands in a map validation already exempts. That is what makes strict binding — and therefore correct duration handling — reachable in the `sources` list at all; see [Configuration Surface](#configuration-surface).

## Package Layout

```
keys/
  config.go        // Config, EntryConfig, source configs, dd.Dynamic decode
  resolve.go       // ResolveConfig — ${VAR} expansion (agora.ResolveConfig precedent)
  validate.go      // boot validation: identities, timings, max_staleness accommodation
  record.go        // Record, hashing, b64token grammar, digest parse, expiry, pattern compile
  source.go        // Source / Watcher interfaces, Contribution
  decode.go        // strict decode pipeline, null normalization, shared envelope types
  store.go         // Store, Snapshot, contribution state, composition, lookup
  refresh.go       // per-source runner, trigger collapse, staleness evaluator
  middleware.go    // bearer auth, context binding, FromContext
  configSource.go  // the implicit first source
  fileSource.go    // file source + fsnotify watch
  httpSource.go    // http source: ETag, count, pagination headers
  meters.go        // OTel instruments
```

**Read that layout as a list of responsibilities, not as prescribed filenames.** It groups by concern; terminus-canon's `camelcase-file-naming` names a Go file in lowerCamelCase after the single primary type it defines, with carve-outs for genuine role files (`config.go`, `middleware.go`, `doc.go`) and for concept-organized packages. Where the two disagree, the canon wins and the grouping above should bend to it. Stage 1 already hit this once: `source.go` held only `Contribution` and became `contribution.go`.

Two later files are likely to hit it again, and are worth naming now rather than discovering at review. `refresh.go` is planned to hold `runner` as its only primary type, which wants `runner.go` unless it genuinely accumulates a role's worth of small declarations. `store.go` is planned to hold both `Store` and the exported `Snapshot`, which may want splitting since `Snapshot` is not a private helper the way the carve-out contemplates. Neither is a design question — decide them against the canon at the time, and expect the file list to end up longer than thirteen.

The package also wants a `doc.go` stating the store-is-sole-owner seam, which the canon lists as an accepted role file. That is where the boundary this package exists to enforce should be legible to whoever arrives next.

What leaves `gateway/`:

| today | becomes |
|---|---|
| `gateway.APIKeysConfig` | `keys.Config` (field stays `Config.APIKeys`) |
| `gateway.APIKeyEntry` | split: `keys.EntryConfig` (wire) and `keys.Record` (domain) |
| `gateway.KeyStore` | `keys.Store` |
| `gateway.NewKeyStore` | `keys.NewStore` |
| `gateway.KeyFromContext` | `keys.FromContext` |
| `gateway.CheckModel` / `CheckRoute` | `(*keys.Record).AllowsModel` / `.AllowsRoute` |
| `gateway/keyStore.go`, `keyStore_test.go` | deleted; content redistributed |

`gateway/handler.go` changes at four call sites (`KeyFromContext`, `CheckModel`, `CheckRoute`, and the `*APIKeyEntry` parameter on `logAndAuthorizeDecision`). Nothing else in `gateway/` touches keys.

The split of `APIKeyEntry` is the spec's second seam made real: `EntryConfig` is one of three wire shapes (config YAML, file YAML, HTTP JSON), and `Record` is what the store holds. Only the mapping layer knows both.

## The Domain Record

```go
// Record is the domain form of a key: what the store holds and what lookup
// matches against. it carries a digest, never plaintext — each source's
// mapping layer hashes a plaintext key and passes a key_sha256 through, so by
// the time a record reaches the store there is one field and no discriminator.
type Record struct {
    Name          string
    Digest        [32]byte
    AllowedModels []string
    AllowedRoutes []string
    ExpiresAt     *time.Time
    Source        string // stamped by the store when the contribution installs
}

func (r *Record) AllowsModel(model string) bool
func (r *Record) AllowsRoute(route string) bool
func (r *Record) Expired(now time.Time) bool
```

`AllowsModel` and `AllowsRoute` are the existing `CheckModel`/`CheckRoute` bodies verbatim, moved and made methods. **Matching behavior does not change** — `path.Match`, `/` still a separator, bare `*` still failing a namespaced id. Only the documentation moves.

`Expired` implements the spec's boundary exactly: a key is valid *strictly before* its timestamp.

```go
func (r *Record) Expired(now time.Time) bool {
    return r.ExpiresAt != nil && !now.Before(*r.ExpiresAt)
}
```

### Key Material

```go
// keyGrammar is b64token from the bearer-token specification — exactly what a
// client can deliver in an Authorization header and get back out unchanged.
var keyGrammar = regexp.MustCompile(`^[A-Za-z0-9\-._~+/]+=*$`)

func HashKey(plaintext string) ([32]byte, error)  // validates grammar, then sha256 over exact bytes
func ParseDigest(hex string) ([32]byte, error)    // 64 hex chars, case-insensitive, compared as bytes
```

`HashKey` hashes the exact bytes as written — no trimming, no case folding, no Unicode normalization — and rejects an empty value. `ParseDigest` uses `hex.DecodeString` after a length check, which is already case-insensitive, and returns the bytes; comparison is therefore byte-wise and a digest differing only in case matches correctly.

Neither function's error ever contains the input.

At lookup the middleware hashes the credential bytes as parsed out of the `Bearer` header, with no preprocessing beyond the existing header parse. **The header parse itself does not change** — `strings.HasPrefix(auth, "Bearer ")` stays as-is, including its case sensitivity. That is shipped behavior and out of scope here.

### Pattern Validation

Every `allowed_models` entry is compiled at load and a pattern that does not compile rejects the source refresh:

```go
if _, err := path.Match(pattern, ""); err != nil {
    return fmt.Errorf("keys[%d].allowed_models[%d]: %w", i, j, err)  // ErrBadPattern — no value
}
```

`path.Match("[", "")` returns `ErrBadPattern`, verified. `allowed_routes` entries are not patterns and get no compile step — only a non-empty check.

## The Decode Pipeline

Both the file and HTTP sources decode through the same three-stage pipeline. The stages exist separately because the spec's null carve-out has to land *between* them.

```go
// 1. intake — syntax rules. duplicate keys, aliases, multi-document,
//    trailing data, non-JSON scalars.
m, err := dd.DecodeStrictYAML(data)   // or dd.DecodeStrictJSON

// 2. normalize — the spec's one carve-out. a null allowed_models,
//    allowed_routes, or expires_at is read as absent, and absent already
//    means unrestricted / no expiry. serializers emit null for an empty
//    collection as a matter of course, and rejecting a refresh over it would
//    fail a management plane that did nothing wrong.
keys.StripNullableRecordFields(records)

// 3. bind — binding rules. unknown fields, required fields, zero coercion.
err = dd.Bind(&doc, m, dd.Strict())
```

`StripNullableRecordFields` deletes only three keys, and only from record objects, and only when the value is explicitly nil:

```go
// StripNullableRecordFields normalizes a list of raw key-record maps in place.
// it takes the record list rather than a document, because the three encodings
// that carry records nest them differently and only this level is common.
func StripNullableRecordFields(records []any)

var nullableFields = []string{"allowed_models", "allowed_routes", "expires_at"}
```

The narrowness is the point. `name: null`, `key: null`, `key_sha256: null`, `version: null`, `count: null`, and `keys: null` all keep rejecting, which is what the spec requires. Verified end-to-end: after normalization `allowed_models` binds as nil, which `AllowsModel` already reads as unrestricted.

**All three decode paths call this one function, including the main config.** The file and HTTP sources locate the record list at `keys`; `LoadConfig` locates it at `api_keys.keys`. Nothing about the carve-out is reimplemented per site.

That uniformity is the whole fix rather than a tidiness preference. Without it the same two lines of YAML behave oppositely depending on which file they sit in — `allowed_models: null` read as unrestricted in `keys.yaml`, and a boot failure in `config.yaml`. The inline-config rejection is pre-existing behavior rather than something this work introduces (`[]string` through the forgiving binder already refuses a nil), but shipping a carve-out that stops at one encoding is what would make it a divergence, and config is increasingly machine-written: a chart rendering `allowed_models: {{ .Values.models | toYaml }}` over an empty list emits `null`, and the gateway would refuse to start.

`LoadConfig` therefore reads the YAML into a `map[string]any`, calls the helper on `api_keys.keys`, and binds from the map, rather than going straight through `dd.MergeYAMLFile`. The rest of the config keeps its forgiving posture untouched.

### Envelope and Record Types

```go
type wireEnvelope struct {
    Version int          `dd:"version,+required"`
    Count   *int         `dd:"count"`            // HTTP only; pointer distinguishes absent from zero
    Keys    []wireRecord `dd:"keys,+required"`
}

type wireRecord struct {
    Name          string     `dd:"name,+required"`
    Key           *string    `dd:"key"`
    KeySHA256     *string    `dd:"key_sha256"`
    AllowedModels []string   `dd:"allowed_models"`
    AllowedRoutes []string   `dd:"allowed_routes"`
    ExpiresAt     *time.Time `dd:"expires_at"`
}
```

`Count` is a `*int` because the spec requires `count` present in the HTTP envelope and a plain `int` cannot distinguish `"count": 0` from an absent field — which is exactly the truncation-reads-as-deny-all failure the spec's requiredness section is about. The file source shares the type and simply requires `Count == nil`; a `count` in a file rejects as a field the file encoding does not define.

**`Key` and `KeySHA256` are `*string` for the same reason, and it is load-bearing rather than stylistic.** As plain strings, "absent" and "present but empty" bind to the identical value, and the natural implementation of the exactly-one-of rule — a non-empty XOR — then *accepts* `{key: "", key_sha256: "abcd…"}`. The spec rejects that document twice over: both fields are present, and an empty key value is rejected. Two decoders would read the same bytes to opposite verdicts with nothing crashing. Verified binding behavior:

```
both absent                  →  key=nil          key_sha256=nil
key: sk-gw-x                 →  key="sk-gw-x"    key_sha256=nil
key: "" + key_sha256: abcd   →  key=""           key_sha256="abcd"   ← the case plain strings erase
key: null                    →  rejected by dd before we look
```

Presence therefore reads straight off the pointer, and `null` never reaches the rule because strict binding already refuses it at a `*string`.

### Mapping to Records

Per record, in order, all errors rejecting the whole refresh:

1. `name` non-empty after no trimming — compared exactly, no case folding, no normalization.
2. Exactly one of `key` / `key_sha256`, decided on **presence** (`!= nil`), never on emptiness. Both non-nil is doubled; both nil is neither; each is its own error, not a precedence puzzle.
3. The one field that is present must be non-empty — a present-but-empty `key` or `key_sha256` is rejected as an empty value, distinct from the doubled and neither errors above.
4. `key` → `HashKey` (grammar-validated); `key_sha256` → `ParseDigest`.
5. Every `allowed_models` pattern compiles; every `allowed_routes` entry is non-empty.

At the envelope rather than per record: `version` must be `1`. Non-negativity is checked explicitly rather than assumed — `dd` binds `-1` without complaint.

Within a single source, two records sharing a digest is a directed error naming both entry names and never the value. This preserves today's `NewKeyStore` behavior and its test.

### Never Log a Secret

The repo's standing rule, extended to every new site strict decoding creates. The rule is **scoped by structural path, not by pipeline stage** — that distinction is the whole content of this section, because scoping it by stage is the mistake that is easy to make and hard to see.

**Our own errors never carry a value.** Grammar failures, digest failures, and the exactly-one-of rule report the record index and field name only.

**No raw `dd` error is logged when its path sits under a record.** `dd` renders values into its errors and honors `+secret` at neither stage, and *both* stages are reachable with a live credential in hand:

```
binding:  key: 12345678       →  expected string, got number 12345678
intake:   key: 0xdeadbeef     →  $.keys[0].key: integer "0xdeadbeef" is not expressible as a JSON number
intake:   key_sha256: 0x9f86  →  $.keys[0].key_sha256: integer "0x9f86" is not expressible as a JSON number
intake:   key: 007            →  $.keys[0].key: integer "007" is not expressible as a JSON number
```

The intake cases are the dangerous half and were missed on the first pass. They fire on an unquoted key shaped like hex, octal, or carrying a leading zero — all values well inside the `b64token` grammar, so a mint can produce one. And they fire *before* binding, so a sanitizer installed only around the bind call never sees them.

So: a single `sanitize(err)` wraps the result of every stage — `DecodeStrictYAML`, `DecodeStrictJSON`, `StripNullableRecordFields`, and `dd.Bind`. It parses the structural path out of the error, and when that path is beneath `keys[n]` it reports the path alone and drops `dd`'s text. Envelope-level failures keep their full text, since no secret field lives there.

This is the pattern the spec arc's close-out named: a fix earns the same scrutiny as the design it repairs. The first version of this rule was written against binding because binding was where the leak had been demonstrated, and it silently under-covered the stage where the leak is easier to trigger.

Tests assert no plaintext key value appears in any error string on any rejection path, at every stage, mirroring `TestKeyStoreDuplicateKeyRejected` — with explicit cases for YAML numeric- and float-tagged values in both `key` and `key_sha256`.

## The Store

```go
type Store struct {
    mu     sync.Mutex // guards states and serializes composition
    order  []string   // "config" first, then declared sources in config order
    states map[string]*sourceState

    snapshot atomic.Pointer[Snapshot]

    // two injected time seams, not one. clock answers "what time is it" for
    // expiry evaluation and staleness ages; newTimer answers "wake me at a
    // deadline" for the exclusion timer. a fake clock alone cannot make a real
    // time.Timer fire, so tests that drive the exclusion deadline exactly —
    // the property a naive test is blind to — need both.
    clock        func() time.Time
    newTimer     func(time.Duration) *time.Timer
    maxStaleness time.Duration
    meters       *Meters
    booting      bool // suppresses composition logging during boot assembly
}

type sourceState struct {
    contribution *Contribution
    loadedAt     time.Time // last load that actually succeeded
    excluded     bool      // dropped from the union for staleness
    reloadable   bool      // false for "config"
}

type Snapshot struct {
    SchemaVersion int
    Generation    uint64
    byDigest      map[[32]byte]*Record
}
```

`SchemaVersion` is 1 in v1 and homogeneous by construction: a source announcing a version the gateway does not understand fails its refresh before it can become a contribution, so a mixed-version union is unreachable. The field exists so that when the delta protocol arrives there is somewhere to record what the union was composed at.

### Composition

This is the piece most likely to be got subtly wrong, so the algorithm is specified rather than described. **Two passes, because precedence decides identity.**

```go
// pass 1 — determine the winning source for every digest across ALL
// contributions, excluded or not.
winner := map[[32]byte]string{}
for _, name := range s.order {
    st := s.states[name]
    if st.contribution == nil {
        continue   // never loaded — see below
    }
    for _, rec := range st.contribution.Records {
        if held, dup := winner[rec.Digest]; dup {
            warn(held, rec)   // both source names, both entry names, never the value
            continue
        }
        winner[rec.Digest] = name
    }
}

// pass 2 — emit a record only when its winner is still in service.
next := map[[32]byte]*Record{}
for _, name := range s.order {
    st := s.states[name]
    if st.excluded || st.contribution == nil {
        continue
    }
    for _, rec := range st.contribution.Records {
        if winner[rec.Digest] == name {
            next[rec.Digest] = rec
        }
    }
}
```

**A nil contribution is a reachable state, not a defensive flourish.** A `required: false` source that fails its boot load never has `Install` called, so its `sourceState` carries a nil contribution for as long as the failure lasts — possibly forever. Both passes must skip it or composition panics on the first recompose, which is to say on the first successful load by *any other* source. The nil check belongs in pass 1 as well as pass 2: a source with no contribution claims no digests, so it can neither win nor suppress.

The two passes are what implement the spec's rule that **a digest whose winner has been excluded is itself excluded**. A single-pass composition that simply skipped excluded sources would promote the suppressed duplicate: a token that authenticated as `breakglass` with the config's permissions would quietly become a lower source's record under a different name and different authority, produced by a knob whose entire purpose is failing closed. The naive implementation is the natural one to write, which is why the shape is pinned here and why a test asserts it directly.

Deletion behaves differently and deliberately so: when a winning source *deletes* a record, that digest leaves pass 1 entirely, so a lower source publishing the same value goes on authenticating it under that source's record. This is the documented promotion the spec scopes rather than prevents — preventing it would require the tombstone that revocation-as-deletion trades away.

After composition, a scan for duplicate `name` values across the union emits an informational warning per repeat. Not an error: a rotation window legitimately produces two records with one name.

An empty composed set is logged loudly at `Warn` — zero keys means deny-all, which is a legitimate operator intent and also what an accidentally truncated file looks like.

**That warning is suppressed until boot assembly finishes.** Contributions are installed one at a time, and a deployment with no inline keys installs an empty `config` contribution before any external source has loaded — so the transient union genuinely is empty, and per-install composition would fire the deny-all warning on every healthy startup, for a snapshot the gateway never serves. The cost is not noise but calibration: an alarm that fires on every boot trains an operator to ignore the one line that means *you have just revoked everyone*. So the store composes in a quiet mode during boot and performs one final composition, with logging, after every initial contribution is collected — which is also the first snapshot that ever reaches a request.

### Installation and Commitment

Loading and committing are separated, which is what makes composition a single store-wide transition rather than a per-source one:

```go
// Install is the only path by which a contribution enters the union. loads run
// concurrently — each source has its own goroutine, and the I/O that actually
// costs time stays parallel — but installation and composition are serialized
// on s.mu, so the union is only ever built from a consistent set of per-source
// contributions rather than from whatever each refresh happened to observe.
func (s *Store) Install(source string, c *Contribution, at time.Time) {
    owned := c.clone()   // defensive deep copy — see below
    s.mu.Lock()
    defer s.mu.Unlock()
    st := s.states[source]
    st.contribution = owned
    st.loadedAt = at
    st.excluded = false
    s.recompose()   // rebuild + atomic.Pointer publish, under the same lock
}

// Touch records a successful confirmation that produced no new data (an HTTP
// 304). it resets the staleness clock exactly as a 200 does, and recomposes
// only when it reinstates an excluded source. a source holding no contribution
// is refused: there is nothing for a confirmation to confirm, and accepting it
// would let a source report healthy while contributing nothing.
func (s *Store) Touch(source string, at time.Time) error
```

Without the lock covering composition, the spec's A₁/B₁ interleaving is live: source A publishes a union composed against the B it saw, source B — which began earlier — publishes one composed against the *older* A, discarding A₁ entirely, and a key A had just revoked is serving again. Every swap atomic, every source correctly serialized against itself, and the revocation silently undone.

**`Install` deep-copies the contribution at the boundary, and the copy is what makes the atomicity mechanical.** Without it the resident snapshot and the source's own working data are the same objects: composition places the source's `*Record` pointers directly into `Snapshot.byDigest`, so a source that reuses a record, a restriction slice, or an expiry pointer while assembling its next load would change names or permissions *piecemeal* in the live snapshot — outside any `Install`, with no generation change, past every validation, and with no lock helping, because data-plane readers take none. A refresh that is atomic everywhere except through pointer aliasing is not atomic.

The clone covers the record structs, both restriction slices, and the `*time.Time` expiry — everything reachable. Neither in-tree source reuses anything, so nothing is broken today; the copy is what keeps that from being a property of two current implementations rather than of the store. Cost is one allocation pass over the key set per refresh, which at a population measured in agents and people is not measurable, and the spec's seam census marks this boundary **enforce** rather than merely record.

**The ownership rule is stated regardless, because a third-party source author cannot see the copy.** A source transfers ownership of the contribution to the store when `Load` returns and must not retain or mutate it afterward. Documenting it tells an implementer the intent; copying it means the guarantee does not depend on their having read this. A test mutates a source-owned contribution after `Install` and asserts lookup is unchanged.

### Lookup

```go
func (s *Store) Lookup(token string) (*Record, bool) {
    snap := s.snapshot.Load()
    rec, ok := snap.byDigest[sha256.Sum256([]byte(token))]
    if !ok || rec.Expired(s.clock()) {
        return nil, false
    }
    return rec, true
}
```

Expiry is evaluated here, not filtered at load. A key whose `expires_at` has passed fails the moment it passes, regardless of when the source last refreshed — filtering at load would leave it working until the next refresh noticed, turning a wall-clock guarantee into one that drifts with the poll interval.

`clock` is injected so tests can drive the boundary instant exactly; it is `time.Now` in production.

`newTimer` is the second half of that seam and exists for the exclusion deadline specifically. Advancing a fake clock does not cause a real `time.Timer` to fire, so a store with only a clock leaves the implementer inventing test-time scheduling for exactly the property round 2 established a naive test cannot see — whether the timer was *armed* to the right deadline, not merely whether the evaluator does the right thing when called by hand. With both seams, arming and delivery are each drivable and no test sleeps. It is `time.NewTimer` in production.

The middleware binds the matched `*Record` onto the request context. Because records are immutable once installed and the snapshot swap replaces the map rather than mutating it, the spec's "snapshot is bound at authentication time" property falls out for free — a request holds the record it matched for its whole life, streaming included.

## Refresh Machinery

One runner goroutine per reloadable source. The config source has no runner.

```go
type runner struct {
    src      Source
    store    *Store
    interval time.Duration
    trigger  chan struct{} // capacity 1
}

func (r *runner) run(ctx context.Context) {
    t := time.NewTimer(r.interval)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            r.refresh(ctx)
        case <-r.trigger:
            r.refresh(ctx)
        }
        // a timer reset after each refresh, rather than a free-running ticker:
        // the interval measures from the end of the last refresh, so nothing
        // accumulates behind a slow load and every trigger source funnels
        // through the same single follow-up.
        if !t.Stop() {
            select {
            case <-t.C:
            default:
            }
        }
        t.Reset(r.interval)
    }
}

// kick requests a refresh. a trigger arriving while one is in flight collapses
// into a single follow-up rather than starting a second, so a burst of fsnotify
// events during a file write costs one extra refresh rather than one per event.
func (r *runner) kick() {
    select {
    case r.trigger <- struct{}{}:
    default:
    }
}
```

The single goroutine is what gives the spec's "at most one refresh running at a time" — serialization by construction rather than by a mutex someone can forget. The accepted cost is that a `SIGHUP` landing mid-poll waits for that poll rather than preempting it.

**Collapse has two halves, and the cap-1 channel is only one of them.** It coalesces *kicks* — a burst of fsnotify events during a file write costs one extra refresh rather than one per event. It does nothing about the poll clock, and a free-running `time.Ticker` has its own independent one-deep buffer: a tick landing during a long load would sit there alongside a queued kick, and the two would produce back-to-back refreshes as soon as the load finished. Resetting a `time.Timer` after each refresh closes that, because the next poll is scheduled from the moment the last one ended rather than on a fixed wall-clock cadence. It also removes the pathology where a load slower than the interval leaves the source permanently refreshing with no idle gap.

The runner owns the whole dispatch, which is the only place a `LoadResult` is interpreted:

```go
func (r *runner) refresh(ctx context.Context) {
    res, err := r.src.Load(ctx)
    if err == nil {
        err = res.validate()                         // exactly one arm, or it is a bug
    }
    now := r.store.clock()
    switch {
    case err != nil:
        if ctx.Err() != nil {
            return                                   // shutdown, not a failure — see below
        }
        r.logFailure(err, now)                       // level chosen from current age
        r.store.recordRefresh(r.src.Name(), "failure")
        // last-known-good stays in place; the contribution is untouched
    case res.IsUnchanged():
        if terr := r.store.Touch(r.src.Name(), now); terr != nil {
            r.logFailure(terr, now)                  // confirmation of nothing
            r.store.recordRefresh(r.src.Name(), "failure")
            return
        }
        r.store.recordRefresh(r.src.Name(), "not_modified")
    default:
        r.store.Install(r.src.Name(), res.Contribution(), now)
        r.store.recordRefresh(r.src.Name(), "success")
    }
}
```

`validate()` runs before the switch rather than inside an arm, because the arms are exactly what is untrustworthy when it fails. An invalid result is logged distinctly as a source bug rather than as an upstream failure, and is then handled as a refresh failure — the runtime behavior stays fail-safe, holding last-known-good, while the diagnostic says plainly that the fault is on this side of the boundary.

**A cancelled context on the way out is not a refresh failure.** `Store.Close()` cancels the store context, so a load in flight at shutdown returns a context error that would otherwise be logged at failure level and counted before the runner ever observes cancellation. The log line is noise; the counter is the part that matters. `llm_gateway.keys.refresh{result="failure"}` is what alerting keys on to notice a management plane going dark, and without this guard every orderly shutdown injects one failure per source into exactly that series — so a rolling deploy would manufacture a burst of them across the fleet, in the signal built to mean the opposite.

Triggers: the poll timer, `kick()` from fsnotify, and `kick()` from `Store.TriggerAll()` on `SIGHUP`.

## Staleness

Three separate concerns, deliberately not sharing a mechanism.

**Age tracking** is `sourceState.loadedAt`, set by `Install` and `Touch` — the last load that actually succeeded.

**The escalating warning** is emitted by the runner on each failed refresh, with the level chosen from the current age. This satisfies "logs at every attempt" without a second timer that would spam. Proposed: `Warn` below ten poll intervals of age, `Error` at or above.

**Exclusion** runs on a timer the store arms to an exact deadline, independent of refresh activity, and only when `max_staleness > 0`:

```go
// armDeadline sets the exclusion timer to the earliest moment any source in
// service will cross max_staleness. it is called at the end of every recompose,
// which already runs on Install, on Touch, and on an exclusion — so every
// transition that can move a deadline rearms as a side effect of the work it
// was already doing. no source near a deadline means no timer at all.
func (s *Store) armDeadline()          // earliest of loadedAt + maxStaleness, over
                                       // non-excluded reloadable sources
func (s *Store) evaluateStaleness()    // fires at that deadline, excludes, recomposes
```

**A sampling ticker is the wrong mechanism here, and the reason is the same one that rules out refresh-coupled evaluation.** Checked only when a refresh attempt finishes, a source stays in the union for up to another poll interval past the configured age, so `1h` quietly means something closer to `1h30m` and the fail-closed bound is not a bound. A ticker reintroduces exactly that failure at smaller magnitude — a contribution surviving up to a full tick past its deadline — and the argument against it does not distinguish on magnitude. An armed timer costs no more code than a ticker, is exact, and does strictly less work.

The independence from refresh activity is required rather than tidy: a source that has stopped answering entirely is exactly the case that must not wait for a refresh to complete before being noticed.

**The test targets the arming, not just the evaluator.** A test that calls `evaluateStaleness` by hand at the deadline passes under either mechanism — it never exercises the scheduling that decides when the evaluator actually runs, which is where the whole defect lived. So the assertions are on what deadline the timer holds after each transition: after a first `Install`, after a `Touch` that moves it out, after an exclusion that hands the earliest deadline to a different source, and after the last in-service source is excluded (no timer armed).

Exclusion **retains** the contribution rather than discarding it, so a `304` after recovery reinstates exactly the records that were set aside. Discarding them would leave a recovered management plane answering the next poll with "nothing has changed" — and it hasn't — leaving those clients denied indefinitely.

The config source is never stale and is skipped by the evaluator: it is boot-resident, which is the whole point of the break-glass key surviving a stale management plane.

## The Sources

```go
type Source interface {
    Name() string
    Load(ctx context.Context) (LoadResult, error)
}

// LoadResult is what a source reports back. a source can succeed in two
// distinguishable ways — it delivered new records, or it confirmed that the
// records already held are still current (an HTTP 304) — and exactly one of
// them is true of any successful load. an implicit encoding, a nil contribution
// with a nil error, would make any future zero-value return path read as "the
// management plane confirmed this," which is the fail-open direction.
//
// the fields are unexported and the two states are reached through
// constructors, so a source cannot assemble a result that claims both at once.
// that combination is not hypothetical tidiness: dispatch has to pick an arm,
// and a result claiming both would have its records silently discarded while
// its freshness advanced — a revocation dropped and max_staleness pushed out,
// which is the exact failure this type was introduced to prevent.
type LoadResult struct {
    unchanged    bool
    contribution *Contribution
}

func Updated(c *Contribution) LoadResult { return LoadResult{contribution: c} }
func Unchanged() LoadResult              { return LoadResult{unchanged: true} }

func (r LoadResult) IsUnchanged() bool          { return r.unchanged }
func (r LoadResult) Contribution() *Contribution { return r.contribution }

// validate enforces the exclusive-or the constructors imply. both arms set and
// neither arm set are equally programming errors; guarding only the empty one
// leaves the dangerous half open.
func (r LoadResult) validate() error

// Watcher is optional. a source that has something native to offer should
// implement it; nothing depends on it, because polling is what makes the
// gateway converge and push is only for latency.
type Watcher interface {
    Watch(ctx context.Context, notify func()) error
}

type Contribution struct {
    SchemaVersion int
    Records       []*Record
}
```

**A source never sees the store.** It reports; the runner dispatches; the store owns the snapshot. That ordering is what keeps the spec's third seam intact — handing a source a store reference so it could call `Touch` itself would be the first bypass, and it would arrive looking like a convenience.

**A source also gives up ownership of what it reports.** Once `Load` returns, the contribution belongs to the store; a source must not retain a reference to it, reuse its records across loads, or mutate anything reachable from it. `Install` deep-copies at the boundary so the guarantee does not rest on that discipline being read — but the rule is part of the interface contract, because an implementer who does not know it will write a source that looks correct and is not.

### Config Source

Implements `Source`, returns the records mapped from `Config.Keys`, and is installed once at boot with no runner. Modeling it as a source rather than a special case keeps composition on one code path.

Config keys are read once and never reload — the correct property for a break-glass credential and a documented limitation for anything else. Hand-authored keys that need to be live-editable are what the file source is for.

`EntryConfig` keeps today's four fields and gains an `+extra` capture:

```go
type EntryConfig struct {
    Name          string         `dd:"name"`
    Key           string         `dd:"key"`
    AllowedModels []string       `dd:"allowed_models"`
    AllowedRoutes []string       `dd:"allowed_routes"`
    Extra         map[string]any `dd:"+extra"`
}
```

**Inline records are strict-bound in a second pass, not merely `+extra`-checked.** `dd.Strict()` cannot cover the main config wholesale — `Merge` does not support strict mode, and the rest of a hand-authored config should keep its forgiving posture — but an `+extra` capture alone delivers only *unknown-field* rejection, which is a fraction of what strict decoding means. The rest of it, and specifically the refusal to coerce, is what the record contract actually depends on:

```
name: 2026-01-01T00:00:00Z   →  forgiving bind stores "2026-01-01"
```

YAML tags that scalar `!!timestamp`, the forgiving binder converts it into the string field, and the operator's name is silently rewritten — truncated, in that example. A name is the metrics series and, once spend limits exist, the budget, and the spec compares names exactly for precisely this reason: no trimming, no case folding, no normalization, because every party can disagree silently and nothing in the auth path ever complains. Meanwhile the identical record in a `keys.yaml` is rejected outright. Same two lines, opposite outcomes, one encoding apart.

So `LoadConfig` runs a second pass: after the forgiving whole-config bind, each raw `api_keys.keys` map is strict-bound into `EntryConfig` and that result replaces the forgiving one. The raw maps are already in hand — `LoadConfig` holds the parsed map in order to run `StripNullableRecordFields` — so this is a loop over a slice already located, and its errors route through the same record sanitizer as every other decode failure.

The `+extra` capture stays, since strict binding and the extra map catch unknown fields by different routes and the capture is what the other four config types rely on; see [Configuration Surface](#configuration-surface).

This is the same lesson as the null carve-out one section down, arriving from the other direction: a property claimed for "every encoding" has to be delivered by every encoding's actual binder, not by whichever mechanism was convenient at each site.

Rejecting unknown fields is also what makes the two deferrals loud rather than silent. `expires_at` in a config key belongs to the `api-key-expiry` card and `key_sha256` to `key-storage`; neither is a field `EntryConfig` declares, so both now produce a directed boot error pointing at the file source, instead of being quietly ignored on a field an operator reasonably believed was doing something.

The grammar check arrives with `HashKey`, applied to the value *after* `${VAR}` expansion.

### File Source

`Load` reads the whole file and runs the decode pipeline. No timeout field — the spec's config surface puts `timeout` only on the HTTP source, and a file is read whole or not at all.

`Watch` uses fsnotify on the **containing directory**, not the file, because editors replace files by rename and a watch on the inode does not survive it. Events are filtered to the target's base name, plus the `..data` symlink swap a Kubernetes ConfigMap mount produces, and debounced at 250ms before a single `kick()`.

Atomic replacement is the supported publication pattern and the docs say so plainly. Debounce narrows the partial-write window; it cannot close it, because a partially-written YAML file can be *valid* YAML — a truncated document that parses cleanly as a shorter key set, or an empty one — so a mistimed read applies a well-formed wrong answer rather than failing. The exposure is bounded, since the next poll converges on the finished file, but it is fail-closed for whoever authenticates during it.

`watch: true` on a path whose watcher cannot be created is a boot error, consistent with explicit configuration the gateway cannot honor failing loudly.

### HTTP Source

`GET {base_url}/v1/keys`, with the `*http.Client` **injected by the gateway** — the `keys` package never constructs an overlay transport, which keeps the agora/zrok wiring in the one place that already owns it and preserves the base-URL-passthrough property that makes TLS ride the tunnel end-to-end.

Request, every poll:

| header | when |
|---|---|
| `Authorization: Bearer <token>` | `token` configured |
| `Cache-Control: no-cache` | always |
| `If-None-Match: <etag>` | an ETag is held |

`Cache-Control: no-cache` is not optional politeness. `If-None-Match` alone does not oblige an intermediary to contact the origin, so a proxy holding a representation it considers fresh could answer the poll itself; the gateway would count that `304` as a successful refresh, reset the staleness clock, and even reinstate an excluded contribution — with nothing having established that the management plane is alive. The whole staleness apparatus rests on `304` meaning *the origin confirmed this*.

Response handling, in order:

1. **Pagination headers, before anything else — including the status dispatch.** A `Link` header carrying `next`, `prev`, `first`, or `last`, or any `Content-Range`, rejects the refresh. A v1 response must not paginate and the gateway does not follow one. This check sits *above* the status switch rather than beside the body, because a `304` carries no body but can still carry headers: a server that has begun paginating announces it on every response, and a `304` accepted as a `Touch` before the headers were read would reset the staleness clock on a source that is already misbehaving.
2. **Status.** `304` → `keys.Unchanged()` **only if this request carried `If-None-Match`** — see below; otherwise it is a refresh failure. The runner turns a genuine `Unchanged` into a `Touch`: a successful refresh that resets the staleness clock exactly as a `200` does and reinstates the source if it had been excluded. `401`/`403` → refresh failure with its own log line, because the gateway's own credential being wrong is an operator problem that will not resolve on its own and should not read like a transient blip. Any other non-`200` → refresh failure.
3. **Body**, read under a size cap, then the decode pipeline.
4. **`version`** must be 1.
5. **`count`** must be present and must equal `len(keys)`. `count` is the cardinality of the *complete authoritative key set, before any response limiting* — never the length of what this response carries. A server that begins paginating and sets `count` to its page length is internally consistent and would otherwise pass.
6. **Only on full success**, set the stored ETag to whatever this response carried — including clearing it when the response carried none.

Step 6 is load-bearing in both directions.

An ETag advanced by a *rejected* `200` would have the gateway send an ETag describing data it declined, receive a `304`, and read it as confirmation of a snapshot it never took — staleness resetting forever while the source was in fact broken, and `max_staleness` quietly disabled by exactly the condition it exists to catch.

An ETag *retained* across an accepted `200` that carried none is the same failure by a different route. The gateway would hold a validator describing a key set it no longer has, send it on the next poll, and a `304` would then confirm a snapshot two generations stale. So the rule is assignment, not update: an accepted `200` sets the stored ETag to exactly what it carried, and carrying none means clearing it.

A `200` without an ETag is otherwise accepted on its merits; the source simply polls unconditionally until some later `200` supplies one. Refusing otherwise-valid keys over a missing cache header would turn a bandwidth question into an authentication outage.

**An unsolicited `304` is a refresh failure, not a confirmation.** A `304` is only meaningful as the answer to a conditional request, and the entire staleness apparatus rests on it meaning *the origin confirmed this specific representation*. With no `If-None-Match` in the request there is no "this," so there is nothing the response can be confirming. Two reachable paths make this concrete rather than theoretical:

- **The first load, at boot.** No ETag is held, so nothing conditional goes out. A server answering `304` anyway would produce a successful refresh for a source that has never delivered a key — and a `required: true` source would then let the gateway *start*, contributing nothing, instead of failing with a directed error. That is precisely the "up and denying everyone while reporting healthy" outcome the fatal-at-boot rule exists to prevent, arriving through the one status code that bypasses it.
- **After an accepted `200` that carried no ETag**, which clears the stored one. The next poll goes out unconditional; a server answering `304` would reset the staleness clock while confirming nothing identifiable, and a server stuck on `304` would hold the clock at zero indefinitely while `max_staleness` never fires.

So the source returns `Unchanged` only when the request it just issued carried `If-None-Match`. Otherwise the `304` is logged as the protocol violation it is and held as a failure. Rejecting it is also simply correct HTTP: a `304` to an unconditional request is not a legal response.

**`Touch` additionally refuses a source that holds no contribution.** This is belt-and-braces against the boot case specifically — the guard above already makes it unreachable through the HTTP source, and this makes it unreachable no matter what any present or future source does. There is nothing coherent for a confirmation to confirm when there is no resident data, and the failure it prevents is a source silently reporting healthy while contributing nothing.

**Every request carries the configured `timeout` as a per-request `context.WithTimeout`, never as `http.Client.Timeout`.** An endpoint that accepts a connection and then hangs costs one interval rather than wedging the loop either way — but the two mechanisms differ in blast radius, and the difference is invisible at the injection site. The agora dialer caches exactly one `*http.Client` per tunnel name and hands the same pointer to every caller that asks for it, so a key source setting `Timeout` on the client it was handed would silently change the timeout for any *provider* reaching through the same tunnel. The client is borrowed, not owned; the source derives a context per `Load` and mutates nothing.

A test asserts the injected client's fields are unchanged after a load, including the timeout path.

## Gateway Wiring

### Construction

In `gateway.New`, after metrics (so the store can build its instruments) and after the agora dialer:

```go
if cfg.APIKeys != nil && cfg.APIKeys.Enabled {
    store, err := g.initKeyStore()   // builds sources, boot-loads, starts runners
    if err != nil {
        return nil, err
    }
    g.keyStore = store
}
```

`initKeyStore` constructs each source in precedence order, injecting an `*http.Client` for an HTTP source that names an `agora_tunnel` (via `g.agoraDial`) or a `zrok_share_token` (via `NewAccess`, passing the source's identity as its label, and appended to `g.accesses` so cleanup already covers it).

**Boot load is fatal by default.** There is no last-known-good at boot, so starting successfully with a key set the gateway knows is wrong means starting into a state where every affected client gets a 401 from a gateway reporting itself healthy — much harder to diagnose than a process that refuses to start with a directed error naming the source it could not reach. A per-source `required: false` is the opt-out and logs at `Error` before continuing.

Runners start at the end of `initKeyStore`, mirroring `multi.StartHealthChecks` — the established pattern for a background loop owned by a component built inside `New`. The store owns its own `context.WithCancel`; `Store.Close()` cancels it, waits for the runners, and closes any fsnotify watchers.

**`initKeyStore` closes its own store on every failure path before returning.** `g.cleanup()` can only reach `g.keyStore` once that field is assigned, which happens after `initKeyStore` returns — so a failure *inside* it, after a watcher was opened or a runner started but before the assignment, would strand those resources with nothing holding a reference to them. In production this is invisible, because a failed boot exits the process. In tests it is not: the suite constructs gateways that fail on purpose, and a leaked fsnotify watcher or a live runner goroutine outliving its constructor is exactly what `-race` and a goroutine-leak check will find. So the local store is `defer`-closed until the function reaches its successful return, at which point ownership transfers to the `Gateway`.

**`Store.Close()` runs first in `cleanup()` — before providers, the zrok share, the zrok accesses, and the agora subsystem.** The ordering is load-bearing rather than cosmetic: a key-source runner can be mid-request against an injected transport at the moment shutdown begins, and tearing that transport down underneath it produces a spurious terminal refresh failure in the logs at best and a race at worst. Stopping and *joining* the runners first means nothing is using a borrowed transport by the time the transports close. This is the reverse of construction order, which is the usual correct answer and is worth stating because `cleanup()` today begins with providers and would naturally be extended at the end.

The lifecycle test covers it: a store with a live runner, `cleanup()` invoked, no refresh attempt observed after the transports close.

### The Signal Loop

`Run`'s signal goroutine currently cancels on the first signal it sees. It becomes a dispatch loop:

```go
signal.Notify(sigCh, append([]os.Signal{syscall.SIGINT, syscall.SIGTERM}, reloadSignals()...)...)

go func() {
    for {
        select {
        case <-ctx.Done():
            return
        case sig := <-sigCh:
            if isReloadSignal(sig) {
                if g.keyStore != nil {
                    g.keyStore.TriggerAll()
                }
                continue
            }
            dl.Info("received shutdown signal")
            cancel()
            return
        }
    }
}()
```

`reloadSignals()` and `isReloadSignal()` live in `gateway/signal_unix.go` (`//go:build !windows`) returning `SIGHUP`, and `gateway/signal_windows.go` returning nothing. Note that `syscall.SIGHUP` *does* compile on Windows — verified — so the split is not about compilation. It is about not registering a signal the platform cannot deliver, and about keeping the intent legible at the call site.

`SIGHUP` with no reloadable sources logs a no-op line rather than staying silent.

### `collectAgoraTunnels`

The journal records this function's invariant: it mirrors `initProviders`' gates exactly so the dialer attaches precisely the tunnels that get wired, and the two must change together. A key source that dials a tunnel has to be collected here or its tunnel is never attached.

Two changes:

```go
// the early return on a nil Providers block must go — a gateway whose only
// tunnel belongs to a key source would otherwise never attach it.
if cfg == nil {
    return nil
}
// ... existing provider gates, now guarded individually ...
if cfg.APIKeys != nil && cfg.APIKeys.Enabled {
    for _, src := range cfg.APIKeys.Sources {
        if h, ok := src.(*keys.HTTPSourceConfig); ok {
            add(h.AgoraTunnel)
        }
    }
}
```

`validateAgora` then requires `agora.enabled: true` for a key-source tunnel automatically, which is the correct and desirable consequence — it reads `collectAgoraTunnels`.

The invariant statement itself widens: **`collectAgoraTunnels` mirrors the init gates of both `initProviders` and `initKeyStore`.** This is worth an amendment to the terminus-canon quality that guards it, since the quality as written names providers only.

### `${VAR}` Expansion

`keys.ResolveConfig(cfg.APIKeys)` is called from `LoadConfig` alongside `agora.ResolveConfig`, and the `api_keys` branch is removed from `Config.expandEnv`.

This splits what the journal records as a single-owner rule — "`Config.expandEnv` at load is the single owner of `${VAR}` expansion." The honest reframing is the one `agora` already established: **one owner per subsystem, all invoked from `LoadConfig`.** `agora.ResolveConfig`/`expandStrings` is the existing precedent and the journal already notes it as the symmetric rule. Scattered expansion at point of use remains forbidden; that is what the rule was actually protecting.

Coverage extends to the HTTP source's `token` and `base_url`, with the same semantics: a value written non-empty that resolves empty is a boot error, and an empty value stays "not configured." A source's *contents* are never expanded — a key file or an API payload is data, not configuration, and `${...}` inside it is a literal.

## Configuration Surface

```go
type Config struct {
    Enabled bool
    Keys    []EntryConfig
    Sources []dd.Dynamic
    Reload  *ReloadConfig
    Extra   map[string]any `dd:"+extra"`
}

type ReloadConfig struct {
    MaxStaleness time.Duration
    Extra        map[string]any `dd:"+extra"`
}

type FileSourceConfig struct {
    Name         string
    Path         string
    Watch        bool
    PollInterval time.Duration
    Required     *bool
    Extra        map[string]any `dd:"+extra"`
}

type HTTPSourceConfig struct {
    Name           string
    BaseURL        string
    Token          string
    AgoraTunnel    string
    ZrokShareToken string
    PollInterval   time.Duration
    Timeout        time.Duration
    Required       *bool
    Extra          map[string]any `dd:"+extra"`
}
```

**Every type in this subsystem carries the `+extra` capture, not just `EntryConfig`.** The rule is that an unknown field anywhere under `api_keys` is a boot error naming the field, and it is uniform because the failure it prevents is worst at the levels furthest from the records. A typo'd `reload.max_stalenes` is silently ignored, leaves `max_staleness` at its unbounded default, and converts an operator's deliberate fail-closed policy into fail-open behavior — with every gauge green and nothing anywhere reporting it. `wach: true` silently disables the file watch; a misspelled `poll_interval` quietly applies the default. These are the same silent-widening failure the record's strict decoding exists to prevent, one and two levels up.

The source configs must **exempt `type`** from the check. `dd.Dynamic` deposits its own discriminator into the `+extra` map — verified, `Extra:map[string]any{"type":"file"}` — so an unexempted check would fail every source on the key `dd` itself put there.

The heterogeneous `sources` list decodes through `dd.Dynamic` with `DynamicBinders` keyed on `type`, which produces directed errors for both an unknown type (`unknown Dynamic type "postgres"`) and a missing discriminator — verified working. `Required` is a `*bool` so an omitted value defaults to `true` while an explicit `false` is distinguishable.

**Timing defaults are preinitialized inside the binder, before the overlay — never applied afterward.** The distinction matters because "omitted" and "explicitly zero" must reach different outcomes: omitted takes the default, explicit zero is a boot error. A default applied *after* binding cannot tell them apart, since both leave the field at `0`, so it would silently convert an operator's `poll_interval: 0s` into a healthy-looking 30s and quietly restore the convergence floor the operator had removed. Preinitializing works because `dd.Bind` leaves a field untouched when its key is absent — verified:

```go
"file": func(m map[string]any) (dd.Dynamic, error) {
    v := &FileSourceConfig{PollInterval: defaultPollInterval}  // 30s
    if err := dd.Bind(v, m, dd.Strict()); err != nil {
        return nil, err
    }
    return v, nil
}

// omitted    → 30s   (default survives, verified under strict)
// 0s         → 0s    (overwrites; rejected by validation as non-positive)
// 5s         → 5s
// -1s        → -1s   (rejected by validation)
```

Omitted and explicitly-zero are tested as separate cases for every timing field.

**The bind inside each source binder is strict, and that is what keeps a unitless duration from meaning nanoseconds.** `time.Duration` is an int64 of nanoseconds, so the forgiving binder assigns a bare YAML number straight through:

```
poll_interval: 30      →  30ns     (and passes a positive-value check)
timeout: 5             →  5ns
max_staleness: 3600    →  3.6µs
poll_interval: 1.5     →  1ns
```

A file source polling every thirty nanoseconds, and a fail-closed deadline of 3.6 microseconds that excludes every source almost immediately. This is not an exotic misconfiguration — it is the *likeliest* one on this surface, because the gateway's existing config already teaches the bare-integer spelling: `health_check.interval_seconds: 30` and `timeout_seconds: 5`, documented in both `configuration.md` and `multi-endpoint.md`. An operator following the project's own convention writes `poll_interval: 30`.

`dd.Strict()` already forbids it — a `time.Duration` field accepts a duration string and nothing else — so no hand-written validation is required, and the diagnostic is better than one we would have written:

```
poll_interval: 30   →  Src.PollInterval: expected duration string, got int
poll_interval: 1.5  →  Src.PollInterval: expected duration string, got float64
```

**Strict binding is reachable here only because of the `+extra` capture above.** `dd.Dynamic` and `dd.Strict()` do not otherwise compose — the strict binder rejects the `type` discriminator that `dd` itself requires, filed as df request 2 — but a struct carrying `+extra` opts out of unknown-field rejection while keeping every other strict rule, so `type` lands in the extra map that validation already exempts. The fix for a different finding is what makes this one free.

**`reload` is strict-bound in a second pass, for the same reason `api_keys.keys` is.** It sits in the main config rather than behind a Dynamic binder, so it would otherwise keep the forgiving posture and `max_staleness: 3600` would still mean 3.6µs.

**One normalization, for the one value where a unit is meaningless.** The spec documents `max_staleness: 0` as the unbounded sentinel, written as a bare number, and strict binding rejects it as it rejects every other bare number. So before the reload block is strict-bound, a `max_staleness` whose raw value is numeric zero is rewritten to `"0s"`. This is not a new exception — the spec already states that `max_staleness` "is the one field where zero carries assigned meaning… the one exception to the rule" — it is that declared exception being implemented. A nonzero bare number is left alone and rejected.

### Validation

All at boot, all directed errors, all in `keys.Validate` except where noted.

| condition | outcome |
|---|---|
| `enabled: true`, no `keys`, no `sources` | error — the surviving usability guard |
| `enabled: false` with `sources` declared | error — explicit config that cannot be honored |
| a declared source named `config` | error — reserved for the inline keys |
| two declared sources sharing a name | error |
| a source name that is empty after trim | falls back to `<type>[<index>]` |
| any timing value written as a bare number rather than a duration string (`poll_interval: 30`) | error from strict binding — `expected duration string`; the sole exception is `max_staleness: 0`, normalized to `"0s"` before binding |
| `poll_interval` or `timeout` zero or negative | error |
| `max_staleness` negative | error |
| `max_staleness > 0` and `≤ poll_interval + timeout` for any source | error |
| file source with empty `path` | error |
| HTTP source with empty or unparseable `base_url`, or a non-http(s) scheme | error |
| HTTP source with both `agora_tunnel` and `zrok_share_token` | agora wins, matching providers; logged |
| a key-source `agora_tunnel` without `agora.enabled` | error, via `validateAgora` |
| unknown field anywhere under `api_keys` — the block itself, `reload`, a source, or a key | error, naming the field; `type` is exempt on a source |
| config key value outside the `b64token` grammar | error, never quoting the value |
| config key with an empty `name` | error |

Identity uniqueness is checked rather than tolerated because the staleness gauge is what makes fail-open revocation degradation alarmable, and two sources sharing a series makes it impossible to tell which contribution went stale. An ambiguous identity disables the observability precisely under the condition it exists to surface.

Timing fields reject zero because a zero duration reads naturally as "no timeout" or "never," and either reading silently removes a property the design claims — a `poll_interval` of zero taken as "never poll" removes the convergence floor; a `timeout` of zero taken as unbounded lets a hanging endpoint wedge that source's loop permanently. Both failures look like a healthy gateway. `max_staleness` is the one field where zero carries assigned meaning — unbounded, the default.

## Metrics and Logging

Four instruments, built from the global meter provider. They are created unconditionally; when `metrics.enabled` is false the global provider is a no-op and the cost is nil.

| instrument | kind | attributes |
|---|---|---|
| `llm_gateway.keys.source.staleness` | Float64ObservableGauge (seconds) | `source` |
| `llm_gateway.keys.source.excluded` | Int64ObservableGauge (0/1) | `source` |
| `llm_gateway.keys.refresh` | Int64Counter | `source`, `result` = `success`\|`not_modified`\|`failure` |
| `llm_gateway.keys.resident` | Int64ObservableGauge | — |

Observable gauges read `sourceState` under the store's lock in their callbacks, so the staleness age climbs correctly without a refresh having to occur — which is the case that matters.

**`staleness` and `excluded` enumerate reloadable sources only.** The `config` source is boot-resident and cannot go stale by design — the exclusion evaluator already skips it — so including it in the gauges would publish an age climbing for the life of the process against something that has no deadline to miss. That is not merely a useless series: it is one every alert rule then has to filter out by name, which is a standing cost paid by whoever writes the alert rather than by whoever emitted it.

**A source that has never loaded successfully emits no staleness observation at all.** This is reachable: a `required: false` source that fails at boot starts with a zero `loadedAt` and may never recover. Both available numbers lie, in opposite directions — an age computed from the zero time reports something around fifty-six years and reads as a broken exporter rather than a real condition, while reporting `0` makes a source that has never delivered a key look perfectly fresh, which is the fail-open direction and precisely what the gauge exists to prevent. Omitting the series is the honest third answer: the absence is itself alertable, it cannot be misread as health, and the degraded state is already carried by the refresh-failure counter and the per-attempt log lines. The `excluded` gauge is omitted for the same source and for the same reason. Once a first load succeeds, both series begin and never stop.

Log lines the spec names, all of which get tests asserting the secret is absent:

- boot summary: source identities, record counts, precedence order
- digest collision across sources: both source names, both entry names, never the value
- duplicate `name` across the union: informational
- refresh failure: source, and for HTTP the status code and structural path — never a body, never a request or credential header
- `401`/`403` from a key API: its own line, distinct from a transient upstream blip
- staleness exclusion and reinstatement
- empty composed snapshot: `Warn`, deny-all stated plainly

### The zrok Share Token Stops Reaching the Logs

A small, deliberate widening of scope beyond dynamic key management, because without it this work ships a key source that violates the invariant the section above restates.

Operator-supplied `zrok_share_token` values are logged today at twelve sites — five in `gateway/access.go` (creating, created, ziti-context error, deleting, deleted), four in `gateway/gateway.go` (OpenAI, Anthropic, single local, and per-endpoint init), and three in `gateway/share.go` on the persistent-share branch (connecting, listener ready, closed). That is pre-existing behavior on every zrok path, not something this work introduces. What this work changes is the class of credential flowing through it: today those tokens front backend transports, while a key source's token fronts the **management plane** — the thing that decides who may authenticate at all. Same mechanism, materially larger consequence.

So the value comes out of every site that prints an operator-supplied token, and the diagnostic keeps a non-secret identifier instead. `NewAccess` takes a caller-supplied label — the provider name, the endpoint name, or the key source's identity — and logs that:

```go
func NewAccess(label, shareToken string) (*Access, error)

dl.Infof("creating zrok access for '%s'", label)     // not the token
```

Every existing call site already has such a label in hand, so this is a signature change and twelve message rewrites rather than new machinery.

**One case deliberately keeps printing a token, and the boundary is the branch that produced it — not the log site.** A share the gateway *generated* has to have its token printed: for an ephemeral share that line is the only way an operator learns how to reach it. That is an output, not a leak. A token the operator *supplied* is a credential, and printing it is.

Getting that distinction wrong by one level is easy, and this work order did on its first pass: `gateway.go`'s single `serving via zrok share '%s'` line looks like the generated case, but `share.Token()` returns whichever branch ran, so the same line prints an operator-supplied persistent token whenever `zrok.share.token` is configured. `gateway/share.go` leaks the same value at three more sites on that branch — `connecting to existing zrok share`, `listener ready for share`, and `share closed` — while its two ephemeral-branch lines are legitimate output.

So the rule is by provenance:

| site | branch | disposition |
|---|---|---|
| `share.go` created / listener ready (ephemeral) | `NewShare` — generated | keep the token |
| `share.go` connecting / listener ready / closed (persistent) | `NewShareFromToken` — supplied | redact, label instead |
| `gateway.go` `serving via zrok share` | either | print only when the share was generated |
| `access.go` lifecycle ×5, `gateway.go` provider/endpoint init ×4 | supplied | redact, label instead |

`Share` therefore records whether its token was generated or supplied, and the serve log line consults that rather than assuming.

Redacting only `access.go` — the path a key source happens to touch — would have left `gateway.go` printing the same value two lines later. That is exactly the site-versus-boundary error this review surfaced six times across the arc, twice inside this section alone, and the reason the fix is repo-wide and scoped by provenance.

A log-capture test asserts a sentinel share-token value never appears across the `Access` lifecycle, including the error and cleanup paths, nor across a persistent `Share` lifecycle — and, in the other direction, that an ephemeral share's generated token *is* still printed.

## Migration and Breaking Changes

Pre-1.0, and the review context accepts breaking changes that buy a better shape. Four are user-visible, and the first three are all one change seen from three angles — inline config keys now decode strictly:

1. **A config key with an unknown field now fails at boot.** Previously ignored. This is the change with the highest chance of stopping an existing deployment, and stopping it is the point: `allowed_model` instead of `allowed_models` silently widened a key to every model.
2. **A config key whose value would be coerced now fails at boot.** Previously the forgiving binder rewrote it — most consequentially a timestamp-shaped `name`, which silently became a different attribution identity than the one written. Rare in practice and loud when it happens.
3. **A config key value outside the `b64token` grammar now fails at boot.** Previously stored and simply never authenticated, because HTTP will not deliver such a value intact. A `sk-gw-` key sits well inside the grammar; this only bites a key carrying spaces, control characters, or non-ASCII.
4. **`api_keys.enabled: true` with no keys** no longer fails when `sources` are declared. The existing check narrows to "no keys *and* no sources" — once the store treats zero keys as deny-all, that check is a usability guard rather than a safety one.

The first three apply **whenever an `api_keys` block is present, including when `enabled: false`.** A disabled block carrying an unknown field or a coercible value now fails at boot rather than being ignored: disabling authentication does not make malformed key configuration valid, and a block that will be switched on later should be found wrong before it is relied on rather than after.

Go API changes (`APIKeyEntry`, `KeyFromContext`, `CheckModel`, `CheckRoute`, `NewKeyStore`) affect no external consumer realistically; the gateway is a binary.

Config files that do not use `sources` and carry clean keys are unaffected.

## Stages

Each lands as its own review, gated by terminus to `clean` before it goes to Michael.

**Every stage writes its own `docs/journal/` entry before it is committed**, per the agent-memory convention — dated entries written as the work happens, with review-on-commit as the gate. Not one entry at the end: a five-stage arc accumulates decisions whose rationale is invisible in the diff, and the stage that made a call is the only place with the context to explain it. Stage 1's entry, for instance, is the only record of why `LoadConfig` uses `dd.Merge` rather than `dd.Bind`.

**Stages are review units on a branch, not releases.** Nothing here reaches a user until the whole work lands, so the bar for a stage is that it is green, coherent, and reviewable in isolation — not that it would be safe to ship on its own. An intermediate tree that has reloadable sources before the staleness machinery arrives is fine, because no deployment ever runs that tree. Read the ordering below as a decomposition for review, and do not reason about it as a sequence of releases.

**Stage 1 — the `keys` package, no new behavior.** Create the package; split `APIKeyEntry` into `EntryConfig` and `Record`; move the store, middleware, and checks; implement hashing, the grammar, and the digest parse; build the config source; rewire `gateway`. Config keys become strict and grammar-validated. Composition exists but has one contribution. The suite is green and gateway behavior is unchanged except for the migration items above.

**Stage 2 — refresh machinery and the file source.** The `Source`/`Watcher` interfaces, the decode pipeline, contribution installation, the two-pass composition with real precedence, the runner and trigger collapse, `SIGHUP` with the platform split, fsnotify with debounce, boot-fatal loading and `required: false`. This is the stage where the spec's concurrency claims become testable, and it is the largest.

**Stage 3 — staleness.** Age tracking, the escalating warning, the independent evaluator, exclusion with contribution retention, the four instruments, and the `max_staleness` accommodation check at boot.

Stage 2 left a deliberate placeholder that stage 3 **must remove**: `Validate` currently rejects any `max_staleness > 0` outright, on the grounds that accepting a fail-closed knob which silently does nothing is worse than refusing it. That was the right call for a tree where exclusion does not exist. It also means `max_staleness` is unusable until this stage lifts the guard and replaces it with the real rule — `max_staleness > poll_interval + timeout`, strictly greater — and the escalating warning is still a flat `Warn` because no age is tracked yet. Both are stage 3's to finish; if the guard survives this stage, the feature ships permanently disabled and looks like a validation bug rather than an unfinished one.

**Stage 4 — the HTTP source.** ETag and `304`, `Cache-Control: no-cache`, the `count` check, pagination-header rejection, the `401`/`403` line, `collectAgoraTunnels` and the zrok path, client injection.

**Stage 5 — documentation and close-out.** The zrok share-token log redaction (`NewAccess` label, `Share` recording token provenance, twelve message rewrites) lands here, since it touches no key-source machinery and wants its own small review. Rewrite `docs/current/api-keys.md` (including the wildcard correction); add `docs/current/key-sources.md` carrying the published contract and the three encodings, with the quote-your-timestamps rule stated beside the `expires_at` field itself rather than in a troubleshooting section; update `docs/current/configuration.md`'s top-level keys and startup sequence; `CHANGELOG.md` under `## Unreleased`. Then the roadmap close-out: `dynamic-key-management` deleted, `api-key-expiry` and `key-storage` bodies narrowed to what this work did not take, both spec and work order removed from `docs/future/` with the still-live deferred items re-synthesized into a smaller document.

Ordering note: staleness lands before the HTTP source even though the HTTP source is where it earns its keep, because a file source goes stale too (a deleted or unreadable file) and that is the cheaper case to test against. Stage 4 exercises it end-to-end.

## Test Plan

| area | cases |
|---|---|
| record | grammar accept/reject table; digest case-insensitivity; expiry at the exact boundary instant; `path.Match` compile rejection; `AllowsModel`/`AllowsRoute` regression of the existing table |
| null carve-out | `allowed_models: null`, `allowed_routes: null`, and `expires_at: null` read as absent in **all three** encodings — file, HTTP, and inline `api_keys.keys` — driven from the same helper; the inline-config cases are tested explicitly, since that path reaches the helper through `LoadConfig` rather than a source |
| decode | unknown field at envelope and record level; YAML and JSON duplicate keys; multi-document YAML; JSON trailing data; missing and null `keys`; missing and null `count`; `count` mismatch; the three null carve-outs binding as absent; `name`/`key` nulls still rejecting; unknown `version` |
| key material presence | the full `key`/`key_sha256` matrix as **separate** cases: both absent, `key` only, `key_sha256` only, both present, `key: ""` alongside a valid `key_sha256`, a valid `key` alongside `key_sha256: ""`, and `null` at either — each landing on its own error rather than collapsing into one |
| zrok token | a sentinel `zrok_share_token` never appears in any log line across the `Access` lifecycle — create, ziti-context failure, delete — nor in provider or endpoint init, nor across a **persistent** `Share` lifecycle (connect, listener ready, close); and in the other direction, an **ephemeral** share's generated token still *is* printed, since that line is how an operator reaches it |
| secrets | no plaintext key in any error string on any rejection path, at **every** decode stage — including intake, with YAML numeric- and float-tagged values (`0xdeadbeef`, `0o755`, `007`) in both `key` and `key_sha256`; no body, header, or credential in any HTTP failure line |
| ownership | a source that mutates its contribution — a record field, a restriction slice, the expiry pointer — after `Install` returns does not change lookup, because the store holds a deep copy |
| boot composition | a deployment with no inline keys and one external source logs **no** deny-all warning on a healthy boot, and does log one when the assembled snapshot really is empty |
| composition | precedence order; collision warning naming both sides and not the value; duplicate-name warning; **winner-excluded does not promote the duplicate**; deletion *does* promote (documented); empty union is deny-all; **a `required: false` source with a nil contribution composes without panicking**, in both passes |
| refresh | burst of triggers collapses to one follow-up, **and a poll tick landing during a long load does not add a second refresh behind a queued kick**; a load slower than the interval still leaves an idle gap; no two refreshes of one source overlap; the A₁/B₁ cross-source interleaving cannot undo a revocation; a failed refresh holds last-known-good; each `LoadResult` arm dispatches to exactly one of `Install` / `Touch` / hold, with the right `result` attribute recorded; **both** invalid `LoadResult` forms — both arms set, neither arm set — are rejected by `validate()` before dispatch, logged as a source bug, and hold last-known-good |
| lifecycle | `Store.Close()` runs before the zrok accesses and the agora subsystem in `cleanup()`, and no refresh is attempted after the transports close; the injected `*http.Client` is unmutated by a load, timeout included; **closing the store with a load in flight logs no failure and increments no `result="failure"` counter** |
| metrics scope | `staleness` and `excluded` carry no series for the `config` source |
| failed construction | `initKeyStore` returning an error after a watcher or runner started leaves no goroutine and no watcher behind — asserted with a goroutine-leak check, since a failed boot exits in production but not in tests |
| staleness | a source that has never loaded emits no `staleness` or `excluded` observation, and both series begin at the first success; the exclusion timer is armed to the correct exact deadline after each transition — first `Install`, a `Touch` that moves it, an exclusion that hands the earliest deadline to another source, the last source excluded (no timer); exclusion fires at the deadline without waiting for a refresh; `304` after recovery reinstates the retained contribution; gauge climbs without refresh activity; boot rejects `max_staleness ≤ poll + timeout` |
| file source | atomic rename picked up; fsnotify debounce; ConfigMap-style symlink swap; unreadable file holds last-known-good; `watch: true` failure is a boot error; **a file produced by marshalling a record with plain `yaml.v3` is rejected with the re-worded error naming the quoting fix**, not with `dd`'s scalar-tag message, and the quoted equivalent loads |
| http source | `200`, `304`, `401`, `403`, `5xx`, timeout; `count` mismatch; `Link` and `Content-Range` rejection, **including on a `304`**; **ETag not advanced by a rejected `200`**; **an accepted `200` carrying no ETag clears a previously held one**; `If-None-Match` sent once held and not sent once cleared; `Cache-Control: no-cache` on every request |
| unsolicited `304` | three separate cases, not one: a `304` on the **first load** fails the source (and a `required: true` source fails boot); a `304` after an accepted ETag-less `200` fails; a `304` answering a real `If-None-Match` succeeds and reinstates an excluded contribution. Plus `Touch` on a source with no contribution returning an error |
| middleware | expired key returns 401 `authentication_error`, indistinguishable from unknown; a request authenticated against snapshot N completes against it after N+1 revokes |
| config | every row of the validation table; an unknown field at each of the four levels (`api_keys`, `reload`, a source, a key) rejects, and `type` on a source does not; every timing field tested omitted *and* explicitly zero as separate cases, **plus a bare positive integer and a fractional number rejected** for each of `poll_interval`, `timeout`, and `max_staleness`, with `max_staleness: 0` still accepted as unbounded; **an inline key with a timestamp-shaped or otherwise coercible `name` is rejected rather than rewritten**, matching what the file source does with the same record |

`httptest` covers the HTTP source; `t.TempDir()` and real writes cover the file source and fsnotify. The injected `clock` and `newTimer` together cover every expiry and staleness boundary without sleeping — the clock for "what time is it," the timer seam for "did the deadline get armed correctly and what happens when it fires." The suite runs under `-race`.

## Critical Files

| file | change |
|---|---|
| `keys/*` | new package; the layout above lists thirteen responsibilities, and the file count lands higher once `camelcase-file-naming` splits them |
| `gateway/keyStore.go`, `keyStore_test.go` | deleted |
| `gateway/config.go` | `APIKeys *keys.Config`; `LoadConfig` binds from a pre-parsed map so `api_keys.keys` reaches the shared null normalizer; `expandEnv` loses its api_keys branch; `collectAgoraTunnels` gains key sources and loses its nil-Providers early return |
| `gateway/gateway.go` | `keyStore *keys.Store`; `initKeyStore`; `Run`'s signal dispatch loop; `cleanup` closes the store first; four zrok init lines stop printing the share token and the serve line prints one only for a generated share, `NewAccess` call sites pass a label |
| `gateway/handler.go` | four call sites move to `keys.FromContext` and the `Record` methods |
| `gateway/signal_unix.go`, `signal_windows.go` | new, platform-split reload signals |
| `gateway/access.go` | `NewAccess` takes a label; five lifecycle log lines stop printing the share token |
| `gateway/share.go` | `Share` records whether its token was generated or supplied; three persistent-branch log lines stop printing it, the two ephemeral ones keep it |
| `go.mod` | `df` v1.0.2; `fsnotify` indirect → direct |
| `docs/current/api-keys.md` | rewritten, wildcard claim corrected |
| `docs/current/key-sources.md` | new — the published contract |
| `docs/current/configuration.md` | top-level keys, startup sequence, shutdown |
| `CHANGELOG.md` | `## Unreleased` |

## Additions Beyond the Spec

Collected so review can check them rather than discover them.

**A response body size cap on the HTTP source.** The spec bounds a hanging endpoint with `timeout` but says nothing about an endpoint that streams forever. Proposed 32 MiB, exceeded is a refresh failure.

**`max_staleness` must be strictly greater than `poll_interval + timeout`, not merely not-less.** The spec says a value that "cannot accommodate" the cadence is rejected; equality accommodates exactly one cycle with zero margin, which any scheduling jitter turns into the flapping the rule exists to prevent.

**Warning escalation thresholds.** The spec says the warning "escalates as the age grows" without naming a boundary. Proposed: `Warn` below ten poll intervals of age, `Error` at or above.

**The staleness exclusion timer is armed to an exact deadline** rather than polled. The spec specifies only that evaluation is independent of refresh activity; it does not say how. Exactness is chosen because the spec's own argument against refresh-coupled evaluation — a stated bound that is not a bound — applies unchanged to any sampling interval.

**`count` as `*int`.** The spec requires `count` present; a plain `int` cannot distinguish absent from zero, which is the exact failure the requiredness rule exists to prevent.

**Quote-your-timestamps on the YAML encoding — the one place this work narrows the published contract, taken knowingly.** The spec defines `expires_at` as RFC3339 and says nothing about quoting; `dd.Strict()` rejects the `!!timestamp` scalar tag at intake, before any hook we control, so a Go management plane marshalling `time.Time` with plain `yaml.v3` emits it unquoted and is refused. Narrowing a contract to fit a library is the wrong direction and is recorded here as such rather than presented as a design choice.

It is nonetheless the right call for now, for three reasons that should be re-checked if any of them stops holding:

- **Relaxing it later is backward compatible.** Accepting additional forms is a widening, not a break, so no version bump and no coordination with anyone already writing against it. Shipping the constraint does not trap us.
- **The blast radius is narrower than "every management plane."** JSON has no timestamp tag, so the HTTP source — which the spec positions as the one a real management plane uses, and the one that makes the extension point pay for itself — is unaffected. This bites specifically a Go program writing the YAML file.
- **The only gateway-side alternative is to reimplement strict YAML intake** — duplicate keys, anchors, multi-document, scalar rules — roughly sixty to eighty lines duplicating library work, and a site-scoped reimplementation of exactly the shared machinery this design has otherwise been consolidating.

Three things follow, and the first two are what keep this from being a footnote an integrator discovers the hard way:

1. `docs/current/key-sources.md` states the rule in the contract itself, beside the field, not in a troubleshooting section.
2. **The file source detects this specific intake failure and re-words it.** `dd`'s message talks about scalar tags, which is accurate and useless to someone who has just written a perfectly reasonable YAML file. The source recognizes a `!!timestamp` rejection under `keys[n].expires_at` and reports a directed error naming the fix — quote the value — with the offending path. A diagnostic that does not name the fix is how a documented constraint becomes an unexplained outage.
3. The removal path is df request 1, already filed and flagged there as the most dangerous of the four. When it lands, the constraint and the re-wording both come out.

**The single-owner expansion rule reframed** as one owner per subsystem invoked from `LoadConfig`, following the existing `agora.ResolveConfig` precedent.

**The `collectAgoraTunnels` invariant widened** to mirror `initKeyStore`'s gates as well as `initProviders`'. Wants a terminus-canon amendment.
