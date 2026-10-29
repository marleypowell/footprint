# atlassian

The HTTP client the Jira and Confluence adapters share. It handles Atlassian Cloud auth (an
account's email and API token, on the site or through the API gateway for scoped tokens), retries
and paging, so neither adapter does its own. `adf/` renders Atlassian Document Format, the rich text
both products return, as Markdown for reading.

What belongs here is anything true of Atlassian Cloud as a whole. Anything true of only one product
belongs in that product's adapter (`internal/tracker/jira`, `internal/wiki/confluence`): JQL, CQL,
field ids and the shape of an issue or page.

Tests run against `httptest` servers and ADF fixtures. They never call a real site.
