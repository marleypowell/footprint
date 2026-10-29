# Handover map

Who else knows what the user knows, for the team they are leaving or the work they are passing
on: a document for their employer, not a record to keep. Window: all. Inputs: `ownership.jsonl`,
`repos.jsonl`, `profile.md`, and the notes of the items on the systems below.

It names colleagues, so it stays inside the company: tell the user, and write it for the people
who will read it. Never put it in the offboarding snapshot or a CV, which carry the user's own
numbers only.

1. **Where the user is the main person.** The areas with `role` `top`, then `major`, that are not
   `archived`, by `living` lines: each with what it is (the `entity` title, else the repo's
   description and the path), `lines`, `living` and `share_percent`, the changes that wrote it
   (`changes`), and the notes of its items for what it does. Skip `repo` areas where a `dir` or
   `entity` area under it says more.
2. **Who else knows it.** For each such area its `others`: name, `lines` and the day of their
   `last` line. Say when the newest is old (a year or more before `as_of`): they may have left, or
   moved on. Where `others` is empty, say nobody else has lines in the files the user changed,
   and ask the user who reviewed or ran it.
3. **What is not in the code.** What only the user can say, per area, asked of them and saved
   verbatim in `data/context/items/<id>.md`: how it is run and deployed, what pages whom, what is
   fragile, who the customers are, what was decided and not written down (the ADRs, `adr+...`).
4. **Open risks.** The areas with a `top` role and no colleague in `others`, the commits blame
   could not attribute (`unattributed_lines`), and the repos with no catalog entity.

State the heads the numbers are as of (`as_of`), and that blame credits a line to whoever changed it
last: a colleague who reformatted or renamed may show as knowing more than they do, and the user as
less. Run `bin/footprint check <draft>` with `window: all`, and cite areas `[[own+<repo>+<path>]]`.
