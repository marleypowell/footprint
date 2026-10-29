# catalog

The seam to the service catalog, which says what each repository is (its systems, components,
APIs, resources and owning groups). This package defines the `Catalog` interface and the neutral
types, and the one adapter, Backstage, lives in `backstage/`.

The neutral types are also output records, so their JSON names are part of the data contract in
`docs/data-schema.md`. Catalog-specific knowledge (file names, annotation names, how entities refer
to each other) belongs in the adapter. A second catalog gets a sibling adapter package and leaves
`internal/collect` alone.

The adapter's tests read catalog files from an in-memory source.
