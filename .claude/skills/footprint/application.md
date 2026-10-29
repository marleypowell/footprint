# A job application

Each application gets two folders with the same `<slug>`, and together they are its paper trail.
`data/context/applications/<slug>/` holds the user's words and the company's: what the company said,
what the user sent, and how each stage went. `check` counts the numbers there as the user's own.
`data/drafts/applications/<slug>/` holds the documents you write. `<slug>` is
`<company>-<role>-<YYYY-MM>` in lower-case kebab, where the month is when the application started:
`acme-senior-swe-2026-10`.

```
data/context/applications/<slug>/
  job.md                 the job description, verbatim, with its link and where it was found
  cv.md                  the CV as sent, verbatim (the text of the file)
  answers.md             the user's answers about this application: why this company and role,
                         salary expectation, notice period, concerns
  process.md             the record: status, people, stages, and a dated log
  stages/<nn>-<kind>.md  one per stage: ## Before (what is known of it) and ## Debrief
data/drafts/applications/<slug>/
  00-brief.md            fit, the CV's backing, the pitch and the story bank (templates/application-brief.md)
  <nn>-<kind>.md         preparation for stage nn (templates/interview-stage.md), each with its .final.md
```

`<nn>` is the stage's number, two digits, and `<kind>` is one of the kinds in
`templates/interview-stage.md` (`screen`, `hiring-manager`, `behavioural`, `coding`,
`system-design`, `deep-dive`, `take-home`). Two coding rounds are `02-coding` and `03-coding`.

`process.md`:

```
---
company: Acme
role: Senior Software Engineer, Platform
status: interviewing
---
## People
| name | role | part |
|---|---|---|

## Stages
| # | stage | kind | when | status |
|---|---|---|---|---|
| 1 | Hiring manager screen | hiring-manager | 2026-10-30 | scheduled |
| 2 | Technical coding #1 | coding | | confirmed |

## Log
- 2026-10-20: applied on the careers site (job id 1234), with cv.md
- 2026-10-24: recruiter call. "the process is 5 interviews ..."
```

The application's `status` is one of `considering`, `applied`, `interviewing`, `offer`, `accepted`,
`declined`, `rejected` or `withdrawn`. A stage's status is one of:

- `expected`: your guess, accepted by the user, that the company has not confirmed;
- `confirmed`: the company named the stage;
- `scheduled`: it has a date;
- `done`;
- `cancelled`.

Outcomes go in the log.

## Rules for every branch

- The log is the paper trail. Every change to the application gets a dated line at the end, quoting
  the user. Earlier lines stay as they are, and a correction is a new line. Write dates as
  `YYYY-MM-DD`, resolving "today" or "next Tuesday" against today's date.
- Save the user's words verbatim, as the rest of the skill does.
- Write every draft at the detail of an interview answer: systems by what they do, customers and
  colleagues by role, and no credentials, internal hostnames, security details or unreleased plans.
- Drafts start with the frontmatter `window: all`. Check and hand them over by `SKILL.md` steps 5 and
  6, with `-plain`.

## Branches

| the user | branch |
|---|---|
| starts an application: a job description, and the CV they sent or will send | Start |
| has news: a recruiter call, a stage booked or changed, an outcome | Record |
| has a stage coming up | Prepare |
| has had a stage | Debrief |
| asks where their applications stand | Status |

When one message covers several (a recruiter call that books stage 1), record the news first.

### Start

1. **Intake.** In one message, ask for whatever is missing: the job description's text or link (you
   fetch a link and save the text), the CV as sent, the date they applied or will apply, how
   (careers site, referral, a recruiter's approach), and what they know of the process. To get a
   PDF or Word CV's text, use the pdf or docx skill. A CV they wrote with this skill is its
   `.final.md` in `data/drafts/`. When they have no CV yet, offer `templates/cv.md` and save it once
   they have sent it. Save `job.md`, `cv.md` and `process.md`. Done when `job.md` holds the text,
   `cv.md` holds the text or the log says it is still to come, and `process.md` has a status and a
   first log line.
2. **The process.** Write the stages the user knows as `confirmed`. When they don't know the
   stages, propose them from the company's own account of its process (search its careers pages,
   and put the URL in the log), or else from what is usual for the role and level. Present it as a
   guess, and write the stages they accept as `expected`. Every stage gets a stage file whose
   `## Before` holds what is known of it. Done when every stage the user knows or accepted has a
   row and a file.
3. **The brief.** Run `SKILL.md` steps 1 to 3 (facts, context, notes), then write `00-brief.md` by
   `templates/application-brief.md`, check it and hand it over. Save the user's answers about the
   application in `answers.md`, and their answers about a work item where step 6 says. Done when
   check exits 0 and the user has the brief's open questions.

### Record

Write the news as a log line in the user's words, then bring the rest of `process.md` into line with
it:

- a stage the company names: `expected` becomes `confirmed`, and a stage it adds gets a row and a
  file;
- a date: `scheduled`;
- what the company says of a stage (format, length, who, what it assesses, material sent): that
  stage's `## Before`;
- people they meet: `## People`;
- an outcome (moved on, offer, rejection): the application's `status`.

With a rejection, ask once whether the company gave feedback, and log what it said. Done when the
status, the stages table and the stage files agree with the log.

### Prepare

1. **The stage.** Its `## Before` needs the date, the format and length, who interviews (their name
   and role), what the company says it assesses, and any material it sent. Ask in one message for
   what is missing, and accept "don't know" as an answer. Done when each is filled in or marked
   "don't know".
2. **Facts and notes.** When `00-brief.md` is missing, write it first (Start step 3). Otherwise
   re-run `SKILL.md` step 1, and step 3 when `todo.txt` has lines.
3. **Draft.** Write `<nn>-<kind>.md` by `templates/interview-stage.md`, check it and hand it over,
   then log `prepared stage <nn>: <final path>`. Done when check exits 0, the user has the final
   file's path and its open questions, and the log line is written.

### Debrief

Offer a debrief when the user says they had a stage, or when a stage's date has passed. They can
decline it.

1. **Ask.** In one message, accepting "skip" as an answer:
   - Who interviewed, and was the stage what you were told?
   - What did they ask, as close to their words as you remember, and what were their follow-ups?
   - Which stories or examples did you use?
   - What went well, and what would you answer differently?
   - What did you learn about the role, the team or the company?
   - What did they say about next steps and timing?

   Ask one follow-up at most. Keep to recording: leave out any rating of how it went and any guess at
   the outcome.
2. **Save.** Put their words under the stage file's `## Debrief`, headed with the date. Mark the stage
   `done` and log the debrief. Anything new they say about a work item (a result, their part) also
   goes in `data/context/items/<id>.md` (`SKILL.md` step 6). When it changes the brief (a new story,
   a gap that turned out to matter), revise `00-brief.md` and check it again. Done when the debrief
   is saved and `process.md` agrees with it.
3. **Carry forward.** In a few lines, tell the user what the next stage's preparation will take
   from this debrief: a question to answer better, a story already told to that interviewer, a gap to
   work on.

### Status

Read every `data/context/applications/*/process.md`. Show one table with the company, role, status,
next stage and its date, and the last log date, with open applications first. Below it, list the
stages whose date has passed and have no debrief, and offer one for each.
