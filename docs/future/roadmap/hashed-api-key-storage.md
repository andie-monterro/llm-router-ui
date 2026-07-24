---
title: hashed api key storage
state: horizon
created: 2026-07-24
tags: [enhancement]
milestone: v0.1.x
---

Store virtual API keys as hashes (e.g. SHA-256) instead of plaintext. Incoming bearer tokens are hashed before lookup, so the config file no longer holds recoverable secrets. `gateway/keyStore.go` already carries a note sketching this option; the tradeoff is that keys can't be read back out of the config.

Listed under "Not Yet Implemented" in `docs/current/api-keys.md`.
