# Application brief

Every stage's preparation draws on this brief, which covers one application. Write it once, and
revise it as debriefs come in. Window: all. The stories lean on the most recent work unless the role
asks for older experience. Inputs:

- the application's `job.md`, `cv.md` and `answers.md`;
- `profile.md`;
- the all-time overview, `facts.json` and `technologies.jsonl`;
- the notes of the L and XL items.

Write `data/drafts/applications/<slug>/00-brief.md` with these sections:

1. **The role.** At most five lines: what the job is, its level, and the requirements that matter
   most, in the description's words.
2. **Fit.** A table with one row per requirement in `job.md` (required, then preferred). Each row
   gives the strongest evidence for the requirement, cited, and rates it: strong, some, adjacent or
   none. The role's stack is checked against `technologies.jsonl` (absent, or not significant). Under
   the table, each requirement rated adjacent or none gets the nearest work the user can speak to,
   cited, and an honest one-line answer.
3. **The CV.** Each line of `cv.md` an interviewer may probe, with the records behind it. For the
   user, list the lines nothing backs, since they will be asked about them. Also list the numbers
   that differ from the facts. `check` takes `cv.md`'s numbers as the user's own, so you compare
   them yourself. When the CV is still to be sent, these lists are what to change in it.
4. **The pitch.** A first-person answer to "tell me about yourself" that takes about a minute to say:
   titles and tenure (`profile.md`), the arc of the work, and the thread that leads to this role.
5. **Story bank.** Eight to twelve stories from the notes, numbered S1 to Sn. Map each one to the
   requirements it shows and the usual questions it answers: a hard problem, a conflict, a failure,
   leading without authority, a decision under uncertainty, feedback given or taken. Tell each in
   situation, task, action and result, with citations, then add the follow-ups an interviewer is
   likely to ask. When a note's open questions leave a story without a result or without the user's
   part, list it for the user. Lead with the user's part and its result. Numbers support a story and
   never stand in for one.
6. **Numbers.** The few totals worth saying aloud, each with its name in `facts.json`.
7. **Open questions** for the user: why this company and this role (when `answers.md` lacks the
   answer), and the gaps above.
