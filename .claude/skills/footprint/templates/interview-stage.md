# Preparing for an interview stage

Material to rehearse from for one stage. It has to stand alone, because the user reads it before
the interview with nothing else open. Window: all. Inputs:

- the stage file, and the `## Debrief` of every earlier stage;
- the application's `job.md`, `cv.md`, `answers.md`, `process.md` and `00-brief.md`;
- `profile.md`;
- the all-time overview and `facts.json`, and the notes the brief cites.

Write `data/drafts/applications/<slug>/<nn>-<kind>.md` with these sections:

1. **The stage.** Who interviews, for how long, in what format, and what the stage tests: the
   company's words first, then what this kind of stage usually probes and which of the role's
   requirements it will likely test. Add a rough time budget.
2. **Carried forward.** From earlier stages' debriefs:
   - the questions already asked, and how they went;
   - the stories already told, and to whom (tell the same interviewer a new one);
   - what interviewers wanted more of;
   - what the user would answer differently;
   - what to follow up on.

   Leave this section out for the first stage.
3. **The body** for the stage's kind (below). It includes the brief's numbers that suit this stage.
   Stories come from the brief's story bank, copied in full and tailored to the stage.
4. **Questions to ask them.** Suited to this interviewer's role, leaving out what earlier stages
   answered.
5. **Open questions** for the user: stories that lack the user's part or a result, and inputs that
   were "don't know".

| kind | the body |
|---|---|
| `screen` (recruiter) | The pitch. Why this company, this role and why now, in the user's words from `answers.md` (ask when it lacks them). The logistics a recruiter asks about, answered in the user's words only: notice period, location and right to work, salary expectation. The process, to confirm with them. Two or three short stories. |
| `hiring-manager` | The pitch. Four to six stories mapped to the role's responsibilities. One system to walk through, shorter than a deep dive. The brief's answers on gaps. What signals the role's level. |
| `behavioural` (values, culture fit) | The company's values or principles, from its own pages (cite the URL) or from the user. Six to ten stories mapped to the values and to the usual questions. |
| `coding` | The format (live editor, pairing, practical task, algorithm questions). The language: the role's language against the user's, from `technologies.jsonl`. The topics to practise, drawn from the role and from what the company says, each with two or three problem types. A way to work aloud: restate the problem, try examples, write a first solution, improve it, test it. One or two pieces of the user's own code in that language that show its idioms, cited. Include stories only when the format has a chat about past work. |
| `system-design` | The likely prompts, from the role's domain. A path through the time: requirements and scale, API, data model, core design, scaling and failure, trade-offs. Two or three of the user's systems as reference designs: what each does, how it is built, the trade-offs taken (their ADRs), and what they would change. The domain's concepts the user has thin evidence for, to read up on. |
| `deep-dive` (past project) | Two or three systems in depth: what each does, how it is built, the user's part, the trade-offs, what failed and what they learned, what they would change, and the numbers. |
| `take-home` (or a presentation) | The task as the company set it, a plan and timeline for doing it, and what it should show about the user. |

For a stage that fits no kind, ask the user what it tests and use the nearest kind.
