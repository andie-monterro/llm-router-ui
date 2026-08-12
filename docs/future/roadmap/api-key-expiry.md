---
title: api key expiry
state: horizon
created: 2026-07-24
tags: [feature]
milestone: v0.1.x
---

Complete the operator ergonomics around API-key expiry.

File and HTTP key records already accept `expires_at`, enforce it at the exact boundary during lookup, and reject an expired key like an unknown one. What remains is:

- expose `expires_at` on boot-resident inline config keys;
- warn operators before a key lapses;
- decide how a record already expired when it loads should be surfaced beyond normal authentication rejection.
