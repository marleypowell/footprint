# Interview preparation

Material to rehearse from for one stage of a job interview. Window: all; the stories lean on the
most recent work unless the role asks for older experience. Inputs: `interview-<company>.md`,
`profile.md`, the all-time overview and `facts.json`, and the notes of the L and XL items.

`data/context/interview-<company>.md` holds the user's own words on the job: the role description as
they have it, the stage (screening, behavioural, technical deep dive, system design, hiring manager,
or another), its format and length, who interviews, and anything the company has said it looks
for. Ask for what it lacks and save the answers verbatim. Name the draft
`interview-<company>-<stage>-<window>.md`, one per stage.

Write at the detail of an interview answer: systems by what they do, customers and colleagues by
role, and no credentials, internal hostnames, security details or unreleased plans.

1. **The stage.** What this stage tests, from the user's description of it, and the role's
   requirements it is likely to probe.
2. **The pitch.** A first-person answer to "tell me about yourself", short enough to say in about a
   minute: titles and tenure (`profile.md`), the arc of the work, and the thread that leads to this
   role.
3. **Stories.** Six to ten stories from the notes, each mapped to a requirement of the role or a
   question the stage is known for (a hard problem, a conflict, a failure, leading without
   authority, a decision under uncertainty). Each in situation, task, action, result, with
   citations, then the follow-ups an interviewer is likely to ask. Where a note's open questions
   leave a story without a result or the user's part, list it for the user to answer.
4. **Deep dives.** For a technical, system design or hiring manager stage: two or three systems
   the user can walk through, from the notes and the docs they wrote: what each does, how it is
   built, the trade-offs taken, and what they would change. Skip this section for other stages.
5. **Gaps.** The role's requirements (its stack against `technologies.jsonl`: absent, or not
   significant) with thin or no evidence, each with the nearest adjacent work
   the user can speak to, cited.
6. **Numbers.** The few totals worth saying aloud, each with its facts.json name.
7. **Questions to ask them.** Drawn from the role description and the stage: the team, the work,
   how success is measured.

Lead with the user's part and its result; numbers support a story, never stand in for one.
