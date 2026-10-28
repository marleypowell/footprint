# Writing a note

A note says what one work item was, in the user's voice, from its pack alone. Every later document
reuses it, so it describes the work and leaves counting to the facts.

Read `data/facts/packs/<id>.md`, then write `data/notes/<id>.md`, starting with the frontmatter the
pack shows at its top:

```
---
item: <id>
inputs: <the pack's hash>
---

## Summary
One or two sentences: what this was and why it existed, as its issues and descriptions state it.

## What I did
The user's part: built, designed, fixed, investigated, led, wrote up. When the pack shows others'
part (sub-tasks done by others, agent or colleague commits, co-authors), say so. Cite with [[id]].

## Outcome
What shipped and what it changed, as the sources state it ("released to all tenants", "replaced
the old receiver"). When they say nothing of impact, write "Not in the data." and ask below.

## Skills
Technologies and practices the evidence shows: languages from the paths, services, protocols,
methods.

## Evidence
The [[id]]s that carry the claims above, most telling first.

## Open questions
What only the user can say: impact in numbers, who it was for, what was hard, their role among
others.
```

- Cite the records in this pack: the item, its issues, its changes, its docs, its reviews.
- A review (`review+...`) is the user reviewing someone else's PR: write it as reviewing ("reviewed
  the retry change and asked for a backoff"), never as the user's change.
- For an ADR, say what was decided and why, and the user's part in it: they wrote most of the
  record, but the decision is the deciders' (often listed in its front matter) unless the text says
  otherwise.
- Describe size in words ("a long-running effort", "a handful of fixes"); numbers come from the facts
  when drafting. Write versions and product names as they are (Angular 15, .NET 8, Office 365):
  check tells them from counts.
- Prefer the user's words in the pack's "From you" section to your inference, and keep their
  corrections.
- For a stale note, revise the existing text for what the pack adds or changes, and keep the rest.
- Write in the first person and past tense, plainly, claiming what the sources support.
- Name customers and colleagues by role ("a large customer", "the platform team"), and keep secrets
  and credentials out.

Done when `bin/footprint check data/notes/<id>.md` shows no errors and no count warnings.
