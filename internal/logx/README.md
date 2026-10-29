# logx

The timestamped progress lines that stages and adapters print to stderr. It is safe for concurrent
use, so parallel work doesn't interleave half-lines.

Progress goes here. Output data goes to files, never to the log, and secrets go nowhere.
