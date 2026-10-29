# wiki

The seam to the wiki the user writes in. It finds the pages and posts they created or edited, and
loads each one's versions and its body as Markdown. This package defines the `Wiki` interface and
the neutral types, and the one adapter, Confluence, lives in `confluence/`.

The neutral types are also output records, so their JSON names are part of the data contract in
`docs/data-schema.md`. Wiki-specific knowledge (search syntax, API versions, body formats) belongs
in the adapter. The wiki is optional, so code using it handles a nil `Wiki`.

This package holds types and interfaces only. The adapter's tests run against `httptest` servers.
