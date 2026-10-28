# CV or LinkedIn profile

Window: all. Inputs: `profile.md` (employer, titles and their dates, team), the all-time overview
and `facts.json`, and the notes of the L and XL items.

**CV.** Per title in `profile.md`, newest first:

- a line with the title, employer and dates;
- four to eight bullets from the biggest initiatives and features in those dates, each an action, its
  object and its result, with citations in the draft;

then a skills line: the significant technologies in `technologies.jsonl`, grouped by category with
their years ("TypeScript, C# (2019-2026)"), most active days first, plus what the notes' Skills
sections add (practices, services, protocols) and the repos' systems.

**LinkedIn.** An About of three to five sentences in the first person, then the same bullets, shorter.

Numbers come from `facts.json`. The version to share is the `-plain` one, without citations.
