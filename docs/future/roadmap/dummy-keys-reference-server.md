---
title: dummy-keys reference server
state: evaluating
created: 2026-08-12
tags: [feature]
---

Add `cmd/dummy-keys`, a standalone binary serving the published `GET /v1/keys` contract from a local file, so the HTTP key source can be exercised without standing up a management plane. Mirror `cmd/dummy-model` in shape: cobra single-root command, `df/dl` logging, plain `net/http`.

Support conditional requests (`ETag` / `If-None-Match` / `304`) and re-read the file per request, so editing keys and watching the gateway converge is the demo. Include failure injection — 5xx, a `count` that disagrees with `keys`, pagination headers, an unsolicited `304`, a stalled response — in the spirit of `dummy-model`'s `--error-rate` / `--error-type`.

## Discussion

Every other part of dynamic key management can be tried with a text editor. The HTTP source cannot: trying it means first writing a throwaway server, which is a toll on anyone evaluating the source the spec positions as the one a real management plane uses.

The failure injection is the point rather than a nicety. The load-bearing behaviours here are all failure behaviours — last-known-good on a failed refresh, the staleness gauge climbing, `max_staleness` exclusion and reinstatement, the unsolicited-`304` guard — and none of them can be demonstrated without a server that misbehaves on request. It is also the only practical way to exercise the overlay paths, since pointing a key source at this through an agora tunnel or zrok share is what covers `keySourceHTTPClient` end to end; unit tests are all in-process.

Do not share the wire structs. `keys`' envelope types are deliberately unexported to hold the store's ownership seam, and exporting them to feed a demo spends a real boundary on convenience. Bind the two sides with a test that starts the server and points a genuine `httpSource` at it, so drift is caught through the bytes a third-party management plane actually implements against — shared structs would agree even while both drifted.

Distinct from the reference database adapter in `docs/future/key-source-follow-ons.md`, which serves the same contract from Postgres or SQLite. This is the barebones step and the natural substrate that one would grow from.
