# Filling in profile.md

`data/context/profile.md` holds what the data cannot know: the employer, the user's titles and
teams with their dates, and anything they say about the arc of their work. Documents take tenure,
titles and teams from it. `footprint check` accepts every number in it as the user's own, so it
holds only what the user said or confirmed.

1. **Read.** Read `data/context/profile.md`, if it exists, and in the all-time `data/facts/overview.md`
   read the coverage dates, the by-year table and the team signals. Done when you know which of
   employer, titles and teams the file lacks, and over which quarters.
2. **Propose teams.** From the team signals, draft the user's team periods. Each period is a run of
   quarters with the same leading board, or the same leading project where there are no boards.
   Treat these as one board: spellings of the same name (`Webteam`, `Web Team`), and names that
   differ only by `Team` or `Board`. These are questions for the user, not periods: a board or
   project with a handful of issues beside a large one (a loan, a stray issue), and projects named
   for shared work rather than a team (backlogs, improvements, known issues). A board's name is the
   tracker's; the team may have gone by another. Done when every quarter of the signals falls in a
   proposed period or a question.
3. **Interview.** In one message, show the proposed periods as a table (from, to, suggested team,
   the boards and projects behind it) with the questions under it, and ask for whatever step 1 found
   missing: employer, every title with its start month, and each team's real name. Ask about the
   quarters before the first signal and after the last, and about gaps. Done when the user has
   confirmed, corrected or rejected every period and answered or declined every question.
4. **Save.** Write the user's answers into `profile.md` verbatim, under `## Employer`, `## Roles`
   (start month and title) and `## Teams` (from, to, team, and what the team did, when they say).
   Keep what the file already had. Confirmed periods go in with the user's names for them. Rejected
   periods and declined questions stay out. Write no issue counts from the signals into the file.
   Done when `profile.md` has the employer, every title with its start, and a team, or the user's
   "don't know", for every quarter from the first counted day to the last.
