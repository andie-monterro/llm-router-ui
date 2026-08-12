---
title: wildcard model restrictions deny namespaced models
state: inbox
created: 2026-08-10
tags: [defect]
---

An API key carrying `allowed_models: ["*"]` is denied any model id containing a `/`. `CheckModel` matches with Go's `path.Match`, where `/` is a separator that neither `*` nor `?` crosses, so `meta-llama/Llama-3-70B` fails against `*` while an absent `allowed_models` passes — it returns before matching at all.

The current API-key and key-source documentation now names this dialect and recommends omitting the field for unrestricted access. Decide whether the behavior should also change by special-casing a bare `*` as unrestricted or moving to a matcher that spans separators.

## Discussion

Bites only where backends serve provider-namespaced ids, which vLLM and SGLang commonly do since they take HuggingFace repo paths directly; Ollama-style short names are unaffected. It fails closed, so the cost is unexpected denial rather than unintended access.

Patterns written with the separator in mind are fine — `claude-*`, `anthropic/*`, and `*/*` all match as expected. The surprise is confined to a bare `*` an operator expects to span every model identifier. The published contract documents the separator property so management planes can validate restrictions consistently; this card retains the usability question of whether the matcher itself should become more generous.
