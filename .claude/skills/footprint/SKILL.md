---
name: footprint
description: Answers the user's questions about their own past work, fills in their profile (titles, teams), and writes their CV, LinkedIn profile, performance review, promotion case, pay rise case, offboarding snapshot or job interview preparation, from the footprint dataset of their commits, PRs, issues, ADRs and wiki pages.
---

# Using the footprint data

Three owners turn the dataset into a document, and each keeps to its part (the rules are in
`docs/facts-schema.md`, also `data/facts/README.md`):

- **facts** (code) decide what counts and how much. Every number you write comes from the facts or
  from the user's own words in `data/context/`.
- **notes** (you) say what each work item was: one per item, written from its **pack**, reused by
  every document after.
- **check** (code) resolves your **citations** (`[[id]]`) and compares your numbers with the facts.

Work from the repo root. Build first when `bin/footprint` is missing or older than the source:
`go build -o bin/footprint ./cmd/footprint`.

## Branches

| the user wants | window | then |
|---|---|---|
| an answer about their work | what the question needs | `questions.md` instead of steps 2-6 |
| their profile (employer, titles, teams) filled in | all | `profile-interview.md` instead of steps 2-6 |
| a performance review | the review period (ask when unstated) | `templates/review.md` |
| a promotion case | since their last promotion (ask) | `templates/promotion.md` |
| a pay rise case | since their last pay change (ask) | `templates/pay-rise.md` |
| a CV or LinkedIn profile | all | `templates/cv.md` |
| an offboarding snapshot | all | `templates/offboarding.md` |
| preparation for a job interview stage | all | `templates/interview.md` |

## Steps

1. **Facts.** Run `bin/footprint facts`, adding `-from YYYY-MM-DD -to YYYY-MM-DD` for a bounded
   window. Read the window's `overview.md`: `data/facts/windows/<from>_<to>/`, or `data/facts/` for
   all time. Done when the command exits 0 and you have told the user the coverage gaps that bear on
   their request.
2. **Context.** Read `data/context/profile.md`, for a review, promotion or pay rise case
   `data/context/framework.md`, for a pay rise case `data/context/pay.md`, and for an interview
   `data/context/interview-<company>.md`. When `profile.md` is missing, or lacks the employer, titles
   or teams the template needs, fill it by `profile-interview.md`. Ask the user for the rest of what
   the template needs and the files lack, and save their answers verbatim in those files. Done when
   every input the template lists is there.
3. **Notes.** Work down the window's `todo.txt`. For each line, read `data/facts/packs/<id>.md` (and
   the existing note, when stale) and write `data/notes/<id>.md` by `notes.md`. With more than 15
   lines, give subagents batches of about 15 ids each, with `notes.md`. Then re-run step 1's command.
   Done when the window's `todo.txt` is empty.
4. **Draft.** Write `data/drafts/<document>-<window>.md` by the template, from the window's overview
   and `facts.json`, and the notes of its items. Start it with the frontmatter `window: <window>`
   (`all`, or `YYYY-MM-DD..YYYY-MM-DD`) between `---` lines. Cite every claim with `[[id]]`. Done when
   every section of the template is filled.
5. **Check.** Run `bin/footprint check <draft>`. Fix every error. For each warning, take the number
   from the facts, cite the record it belongs to, or keep it and list it for the user as unverified.
   Done when check exits 0 and every warning is fixed or listed.
6. **Hand over.** Run `bin/footprint -plain check <draft>` for a CV, LinkedIn profile, offboarding
   snapshot or interview preparation, `-links` for a review, promotion or pay rise case, to write
   `<draft>.final.md`. Give the user its path, the unverified numbers, and the open questions of the
   notes it drew on. Save their answers verbatim in `data/context/items/<id>.md`: the next facts run
   adds them to those items' packs, and the notes go stale to take them in.
