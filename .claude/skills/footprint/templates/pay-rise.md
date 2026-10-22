# Pay rise case

The case for more pay in the user's current role. Window: since their last pay change (ask; save it
in `profile.md`). Inputs: `pay.md`, `profile.md`, `framework.md` (their current level's
expectations, when they have one), the window's overview and `facts.json`, and the notes of its
items.

`data/context/pay.md` holds the user's own words on pay: their current pay, the rise they want,
their market evidence with each figure's source and date, and how their employer reviews pay
(cycle, bands, who decides). Ask for what it lacks and save the answers verbatim: every pay figure
in the draft comes from it, and `check` accepts its numbers as the user's own. Market figures are
the user's to gather (salary surveys, recruiter conversations, offers, published bands). Where they
have none, the case rests on value and scope, and the Market section says so.

1. **The ask.** One paragraph: the role, the time since the last pay change, the rise asked for (from
   `pay.md`), and its grounds: value delivered, scope grown beyond the role, the market.
2. **Value delivered.** The three to five biggest outcomes in the window: what each was, the user's
   part, and what it changed for the business, in the user's words where the sources say nothing
   (`items/<id>.md`), with citations.
3. **Scope beyond the role.** Where the window's work goes beyond the current level's expectations
   in `framework.md`, or beyond the work of their title's first year (`profile.md`, the by-year
   table): initiatives they drove, repos they started (`repos.jsonl` -> `started_by_me`), systems
   they worked across, work they raised for others, docs that shaped decisions.
4. **Market.** The user's market evidence from `pay.md`, each figure with its source and date.
5. **Numbers.** A short table of the window's totals, each with its facts.json name.
6. **Questions a manager will ask.** Why now, what changes if it is granted or refused, what comes
   next, and the open questions of the notes the case draws on, for the user to answer before
   sharing.

Lead with outcomes and scope; totals support them.
