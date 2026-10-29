# envfile

Loads `KEY=VALUE` lines from the repo's git-ignored `.env` into the process environment, for the
tokens the adapters read. Values that are already in the environment win.

It never logs or returns values, only names. Keep it that way: the tokens are the user's.
