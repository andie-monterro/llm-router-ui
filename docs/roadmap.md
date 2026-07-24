# Roadmap

This project's roadmap is a set of files in the repo, not a forge project board. It lives in [`docs/future/roadmap/`](future/roadmap) — one markdown file per item, each a small, self-contained prompt for work to be done. Anyone who can edit a file is a first-class participant: a maintainer in an editor, an agent in a coding harness, or `grep`. No board, no credentials, no tooling required — the files are the source of truth.

The forge issue tracker still exists as an inbox where outside users start conversations; roadmap-shaped material gets pulled across by a maintainer, carrying a `source:` line back to the original issue.

## An item

One markdown file: YAML frontmatter carries the machine-readable spine, the body carries whatever prose the idea has right now — one line for a raw thought, pages for a matured design.

```yaml
---
title: retry semantics
state: inbox
created: 2026-07-24
tags: [feature]
source: github:openziti/llm-gateway#412
log:
  - stamp: 2026-07-24
    note: spec drawn — docs/future/retry-spec.md
---

body prose, at whatever weight the idea currently has.
```

| field     | type                                       | required |
| --------- | ------------------------------------------ | -------- |
| `title`   | string                                     | yes      |
| `state`   | one of the five states below               | yes      |
| `created` | date, `YYYY-MM-DD`                          | yes      |
| `tags`    | list of strings (the label set below)      | no       |
| `source`  | string — provenance, e.g. a forge ref      | no       |
| `log`     | list of `{stamp: date, note: string}`      | no       |

Unknown fields are tolerated and preserved. Items never nest; a cluster that wants to travel together is what `tags` are for.

**The filename is the slug of the title.** The rule is mechanical so every writer derives the same name: lowercase `A`–`Z`; keep `a`–`z`, `0`–`9`, space, and hyphen; discard every other character; convert spaces to hyphens; collapse hyphen runs; trim leading and trailing hyphens. `Retry Semantics (v2)` becomes `retry-semantics-v2.md`. Never overwrite an existing file — a name collision means retitle the newcomer.

## The states

Any state may move to any state; the lifecycle is descriptive, not enforced.

- **inbox** — untriaged. Where every capture lands.
- **horizon** — triaged and deliberately at rest. Longer-term or vision-shaped material lives here, possibly for a long time, legitimately.
- **researching** — being shaped: design work, a spec possibly being drawn.
- **building** — implementation in flight.
- **evaluating** — built and being lived with; soak.

There are no terminal lanes. An item is a *prompt* — the unit of work-to-be-done. When a prompt is realized and holds through soak, it is **deleted**, and its information is synthesized into the project first: `docs/current/`, the `CHANGELOG.md`, the code itself. Declined work is deleted the same way. Git history keeps the archaeology in both cases, so the working tree never accumulates residue.

## How to participate

**Capture** is a plain file write: create `docs/future/roadmap/<slug>.md` with `state: inbox`, today's date, a title, and a body. That is the whole gesture. Read a sibling item first for the shape.

**Edits are surgical:** change only the lines that express your change and leave every other byte alone — no field reordering, no requoting, no reformatting. The clean, reviewable diff is the load-bearing safety property of the whole design.

**Everything written is a proposal.** The working tree is the write buffer and git is the judgment gate: a new or edited item shows up in the next `git status` and gets read, adjusted, committed, or discarded by a maintainer. That is exactly why anyone may participate freely.

An optional terminal reader, [ranger](https://github.com/michaelquigley/ranger), renders the board (`ranger list`), flips states (`ranger state <slug> <state>`), and captures headlessly (`EDITOR=true ranger "<title>"`). None of it is required; it is a reader over the files.

## Rules for agents

- **Never touch `order.yaml`.** Priority is a maintainer's judgment about energy and context, assigned at triage. New items land unranked; position is not an agent's call.
- **Never commit or push roadmap changes** unless explicitly directed. The uncommitted diff is the review queue.
- **Never delete items on your own judgment.** Deletion is the maintainer's curation gesture — retiring realized work, dropping declined work, clearing duplicates. An agent may perform a deletion when directed as part of a close-out (synthesizing realized work into `docs/current/` and the changelog first); it never initiates one.
- **Don't use `log:` as a changelog.** It is sparse, dated, one-line stamps for the few moments worth keeping — chiefly a spec being drawn on the item. Git history is the archaeology for everything else. Point a `log:` stamp at a specific journal entry when a card leans on hard-won context.

## Label vocabulary

Tags are soft grouping, optional. Apply the one that fits; don't invent near-synonyms.

- `defect` — something shipped is broken.
- `documentation` — docs work.
- `enhancement` — an improvement to something that exists.
- `epic` — large scope; an umbrella that will likely spawn multiple items or a spec.
- `feature` — a new capability.
- `spike` — a time-boxed investigation; the deliverable is understanding, not code.
- `story` — user-story-shaped work.
