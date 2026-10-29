# careers

Reads `data/careers.json`: the user's datasets, one per job (or per org at one job), and one for
their own projects, marked personal. It checks the file, says where each dataset's files are
(`Paths`) and what is on disk (`Kind`: raw data, facts alone, or nothing but the user's words). The
CLI uses it to point its path flags at one dataset (`-dataset`), to keep collection out of a
frozen one, and to mark one frozen (`freeze`).

It reads no dataset and decides nothing about what counts: that stays in `internal/facts`, run on
one dataset at a time. Without `careers.json` nothing here runs, and the dataset under `data/` is
the only one.

The tests are table-driven over the file's rules and the paths it gives; the kinds are tested
over directories made in a temp dir.
