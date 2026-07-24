---
title: gateway-side cost metering
state: horizon
created: 2026-07-24
tags: [feature]
milestone: v0.1.x
---

Meter per-key token attribution and streaming usage at the gateway, so a cost ceiling can be enforced here rather than in each client harness. The gateway currently meters nothing usable per client — no dollars, no per-client tokens, streaming unmetered — which is why downstream governed runs enforce spend caps themselves. The request path already allows the enforcement locus to migrate without a client-facing contract change.
