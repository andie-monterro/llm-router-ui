---
title: key storage
state: researching
created: 2026-07-24
tags: [enhancement, spike]
milestone: v0.1.x
---

Finish the plaintext-free storage story for virtual API keys.

## Discussion

The gateway's resident store now holds only SHA-256 digests, hashes incoming bearer tokens before lookup, and accepts `key_sha256` from file and HTTP sources. External management planes can therefore keep no recoverable gateway secret, and the HTTP source already adapts database- or Vault-backed stores without putting their drivers in the gateway.

The remaining plaintext path is boot-resident inline configuration, whose schema accepts only `key`. Decide whether to add a digest form there, remove plaintext inline keys entirely, or provide a purpose-built local store with a separate security posture. Key creation, recovery, rotation history, and any write API remain management-plane concerns rather than responsibilities of the resident read-side store.
