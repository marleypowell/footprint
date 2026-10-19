# Offboarding snapshot

A record of what the user did at the company, to keep once access to its tools ends: standalone
text that needs no links. Window: all. Inputs: the all-time overview and `facts.json`, every
required note (the all-time `todo.txt` empty), `profile.md`.

Before writing, tell the user the snapshot draws on company systems, and ask them to check what
their employer lets them keep. Write at the detail of an interview answer: systems by what they do,
customers and colleagues by role, and no credentials, internal hostnames, security details or
unreleased plans.

1. **Summary.** Tenure, roles, and the arc of the work (`profile.md`, the by-year table).
2. **Timeline.** By year, the main initiatives and features, from the notes.
3. **Systems.** The systems the user built or owned (`repos.jsonl`: `started_by_me`, `systems`),
   with what each does.
4. **Skills and technologies.**
5. **Writing.** The docs they wrote, and what each was for.
6. **Numbers.** The all-time totals, each with its facts.json name.

Then go through `data/facts/catalogue.md` (every item with its note) with the user, for anything they
may not keep. Give them the `-plain` final file and the catalogue to keep together.
