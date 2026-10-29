# codehost

The seam to the service that hosts the org's git repositories. This package defines the `Host`
interface and the neutral types (repos, pull requests, reviews), and the one adapter, GitHub, lives
in `github/`.

The neutral types are also output records, so their JSON names are part of the data contract in
`docs/data-schema.md`. Host-specific knowledge belongs in the adapter: API calls, URL shapes, ref
names, and how a merge commit names its pull request. A second host (GitLab, say) gets a sibling
adapter package and leaves `internal/collect` alone.

The adapter's tests run against `httptest` servers. They never call GitHub or the `gh` CLI.
