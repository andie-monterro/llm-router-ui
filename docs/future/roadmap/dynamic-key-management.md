---
title: dynamic key management
state: researching
created: 2026-07-24
tags: [feature, spike]
milestone: v0.1.x
---

llm-gateway needs to grow a facility for dynamic key management. we can retain the facility to express keys in the config file. additionally, llm-gateway grows support for an extension point allowing keys to come from another data source. it will ship with an implementation that uses an external yaml file, and a unix signal can be used to provoke llm-gateway to re-read the external yaml file.

## Discussion

Runtime management couples to hashed storage and expiry: a key minted at runtime needs the same storage shape and lifecycle as a configured one, so those land first or alongside. The unknown the spike is for is the admin surface's own auth boundary — who may manage keys and how they authenticate — which must be distinct from the data-plane `sk-gw-*` keys it manages.
