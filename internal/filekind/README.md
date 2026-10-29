# filekind

Tells the files a person wrote from those a tool generated or that came from elsewhere (vendored
code, lockfiles, minified bundles, snapshots), by path and size. Line counts leave the second kind
out, so a regenerated lockfile doesn't read as a thousand lines of work.

It is decided from the path and size alone, with rules compiled in, so the same file is classed the
same way everywhere it is counted.
