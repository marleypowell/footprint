# doctor

Says whether a collected dataset is complete and, for each gap, what to run or change. Examples:
commits the code host credits to the user that the dataset lacks, identities that may be the user,
issue keys that failed to load, and output older than its inputs. `footprint doctor` exits 0 only
when nothing is left to fix.

It reads the dataset, the run state in `cache/` and the config if there is one. It calls no tools
and reads the names of token variables but never their values, so it also works on a copy of a
dataset. New checks belong here when they judge completeness. Judging what the data means belongs
in `internal/facts`.

Tests build small datasets in a temp dir.
