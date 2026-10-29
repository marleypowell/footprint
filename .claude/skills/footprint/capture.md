# A weekly capture

What a week's work led to is cheapest to record the week it happened, and the tools record almost
none of it. This asks the user a few short questions and saves their words. It writes no notes and
no documents.

1. **Refresh.** Run in the background `bin/footprint mirror prs reviews commits projects verify issues pages`
   (`.exe` on Windows), then `bin/footprint doctor`. On a FAIL, tell the user what it found before
   asking anything; if the refresh itself fails, go on with the data there is.
2. **Since when.** The newest `data/context/log/*.md` has `window: <from>..<to>` in its frontmatter:
   `<to>` is the start. With no log, ask the user, defaulting to 14 days ago.
3. **Off-tool question.** While the refresh runs, ask once: "Anything this week the tools don't
   show: incidents, mentoring, design talks, interviews, helping other teams?"
4. **Facts.** Run `bin/footprint facts -since <start>` and read `data/facts/recent/capture.txt`.
5. **Items.** Send the user one message with at most 8 items, numbered, from the top of the list: id,
   title, and what the facts say (landed, resolved on a date), nothing else, no summary. Put the
   rest on one line ("also: ..."), and ask whether any matters.
6. **Questions.** At most three per item, and "skip" or "don't know" are answers:
   - Where does it stand now? What happened after it shipped?
   - Who used it, or was affected?
   - Is there a figure, and where could someone check it?
   - When others were involved: what was your part, and whose was the rest?

   No adjectives. Never offer a number or a likely impact. Never restate an answer in grander words.
7. **Save.** Their words, verbatim. Item answers are appended to `data/context/items/<id>.md` under
   `## <year>-W<week>` (they join the item's pack, and its note goes stale to take them in);
   everything else goes in `data/context/log/<year>-W<week>.md`:

   ```
   ---
   window: <start>..<last day in capture.txt>
   asked: <ids asked about>
   ---
   <what the user said about work that is no item>
   ```

   Write the log last: it marks where the next capture starts.
8. **Rebuild.** Run `bin/footprint facts`.

A review, promotion or pay rise case reads the logs inside its window.
