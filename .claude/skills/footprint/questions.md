# Answering a question

1. Run `bin/footprint facts`, with `-from`/`-to` when the question has a period.
2. Find the records with plain grep: `data/` is git-ignored, so search tools that follow
   `.gitignore` skip it. For example,
   `grep -ril 'search term' data/notes data/facts/items.jsonl data/facts/docs.jsonl data/facts/changes.jsonl data/facts/reviews.jsonl`.
   Read the matching notes, and the packs (`data/facts/packs/<id>.md`) of items without one. For
   detail beyond them, follow the paths in `data/raw/index.json`.
3. Take numbers from `facts.json` (the window's, for a period) or an item's `totals` in
   `items.jsonl`. For any other number, compute it with `jq` over `data/facts/*.jsonl` and show the
   command in the answer, so it can be run again.
4. Answer with dates, ids and links (the records' `url` fields). When the question touches what was
   never collected (`facts.json` -> `coverage.not_collected`), say so.

Done when every claim in the answer names its record and every number its source.
