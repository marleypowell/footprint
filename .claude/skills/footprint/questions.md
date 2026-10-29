# Answering a question

1. Run `bin/footprint facts`, so the packs are current.
2. Find the records with `bin/footprint search <term>...` (every term must match; quote a phrase),
   narrowed by `-from YYYY-MM-DD -to YYYY-MM-DD` when the question has a period, `-repo`, `-item`
   and `-kind item,issue,change,doc,review`. Each line is `kind, id, item, first..last, title, url`.
   Try a synonym or a narrower term when a search finds nothing or too much. Read the notes
   (`data/notes/<item>.md`) of the items it names, and the packs (`data/facts/packs/<id>.md`) of
   items without one. Who owns what (the repos, catalog entities and directories where the user's
   lines still stand) is in `data/facts/ownership.jsonl`: grep it for the area by name. For detail
   beyond them, follow the paths in `data/raw/index.json`.
3. Take every number from `bin/footprint count`, with the same `-from`/`-to`, `-repo` and `-item`
   flags, `-tech <name>` for a language, tool or platform, and `-by year|quarter|repo|item` for a
   breakdown, and show the command in the answer so it can be run again; an area's lines and share
   come from its record in `ownership.jsonl`. Count no records by hand or with `jq`: when neither
   has the number asked, tell the user the facts don't count it.
4. Answer with dates, ids and links (the records' `url` fields). When the question touches what was
   never collected (`facts.json` -> `coverage.not_collected`), say so.

Done when every claim in the answer names its record and every number its source.
