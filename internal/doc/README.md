# doc

Raw documents kept as their authors wrote them: JSON-ready values whose objects keep their key
order and whose YAML scalars keep their text. A catalog file or a project manifest comes out of the
dataset looking the way it was written, not re-sorted or re-typed by `encoding/json`.

It knows nothing of footprint's tools or records. It is a small value library that the adapters and
stages build on.
