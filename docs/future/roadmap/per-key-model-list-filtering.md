---
title: per-key model list filtering
state: horizon
created: 2026-07-24
tags: [feature]
milestone: v0.1.x
---

Filter `/v1/models` responses per virtual API key, so a key only sees the models its `allowed_models` patterns permit rather than the full advertised set. Today the model list is the same for every caller regardless of restriction.

Listed under "Not Yet Implemented" in `docs/current/api-keys.md`.
