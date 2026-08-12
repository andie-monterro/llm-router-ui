---
title: single agora tunnel-consumer gate
state: inbox
created: 2026-08-12
tags: [enhancement]
log:
  - stamp: 2026-08-12
    note: raised by terminus during dynamic-key-management stage 5; vetoed there — docs/journal/2026-08-11.md
---

Derive agora tunnel attachment and consumer initialization from one shared gate, so `collectAgoraTunnels` no longer has to mirror `initProviders` and `initKeyStore` by hand. Today the agreement between them is a convention held by review rather than by construction.

## Discussion

No defect drives this. The mirror has held across both arcs that touched it, and the cost so far is a review finding whenever a new consumer appears — dynamic key management added the third one and needed the guarding terminus quality widened to name it.

That is the trigger worth watching rather than the work itself: a fourth tunnel consumer, or the first time the two sides actually drift in a way review does not catch. Either turns a convention into a defect and makes this worth the cost, which is real — it reopens shipped agora wiring for a correctness property that is currently holding.

If it lands, the `attachments-mirror-wiring` quality retires rather than being violated; its boundary section already says a single shared gate dissolves the obligation.
