# tracker

The seam to the issue tracker. It recognises the keys of the work items that commits, branches and
pull requests mention, and loads those work items with their comments, history and worklogs. This
package defines the `Tracker` interface and the neutral types, and the one adapter, Jira, lives in
`jira/`.

The neutral types are also output records, so their JSON names are part of the data contract in
`docs/data-schema.md`. Tracker-specific knowledge belongs in the adapter: key formats, query
languages and custom field ids. The tracker is optional, so code using it handles a nil `Tracker`.

This package holds types and interfaces only. The adapter's tests run against `httptest` servers.
