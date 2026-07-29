---
title: per-key metrics
state: researching
created: 2026-07-24
tags: [feature, spike]
milestone: v0.1.x
log:
  - stamp: 2026-07-29
    note: spec drawn — docs/future/streaming-token-usage-capture.md
---

per-agent (per `sk-gw-*` key):

- Request volume per agent.
- Token usage (prompt + completion) per agent.
- Model usage per agent (which models each agent calls).

Request volume and model usage already record per `key`. Token usage does not: the
non-streaming counters carry no `key` label, and the streaming path — the default
for agents — records no tokens at all. Spec covers closing both.
