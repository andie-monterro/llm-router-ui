---
title: wildcard model restrictions deny namespaced models
state: inbox
created: 2026-08-10
tags: [defect]
---

An API key carrying `allowed_models: ["*"]` is denied any model id containing a `/`, despite `docs/current/api-keys.md` stating that `["*"]` and an absent `allowed_models` both mean unrestricted access. `CheckModel` matches with Go's `path.Match`, where `/` is a separator that neither `*` nor `?` crosses, so `meta-llama/Llama-3-70B` fails against `*` while the absent case passes — it returns before matching at all. The two forms the docs call equivalent are not.

Decide whether the fix is to special-case a bare `*` as unrestricted, move to a matcher that spans separators, or narrow the documentation to what `path.Match` actually does.

## Discussion

Bites only where backends serve provider-namespaced ids, which vLLM and SGLang commonly do since they take HuggingFace repo paths directly; Ollama-style short names are unaffected. It fails closed, so the cost is unexpected denial rather than unintended access.

Patterns written with the separator in mind are fine — `claude-*`, `anthropic/*`, and `*/*` all match as expected. The surprise is confined to a `*` an operator expects to span a `/`, which is exactly the bare-`*` case the docs single out as unrestricted. Surfaced while specifying the published key-record contract in `docs/future/dynamic-key-management.md`, which now names the dialect and the separator property so a management plane authoring restrictions is not surprised by them; that spec deliberately does not change the matching behavior itself.
