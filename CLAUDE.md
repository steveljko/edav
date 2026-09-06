# Working agreement

A self-hosted CalDAV/CardDAV/WebDAV server in Go: one static binary, SQLite by
default, no runtime dependencies. Lighter alternative to Davis/sabre-dav.

## Comments

Sparingly, and only where the code cannot explain itself. Comment a non-obvious
protocol requirement, a workaround for a specific client's behaviour, a
performance trade-off, or a link to the relevant RFC section.

Do not restate what the next line does. No section-header banners, no docstring
on every function, no comments narrating the development process. Exported
identifiers get doc comments where the package is meant to be consumed;
internal glue usually does not.

## Attribution

No AI attribution anywhere: no generated-with footers, no `Co-Authored-By`
trailers, no mention of AI in commits, comments, README, or docs.

## Git

Single `main` branch. No feature branches, no PRs. Commit directly to `main`.

Commits follow Conventional Commits v1.0.0:

```
<type>[optional scope]: <description>

[optional body]

[optional footer]
```

Types: `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `build`, `ci`,
`chore`. Scopes are package or subsystem names: `caldav`, `carddav`, `storage`,
`auth`, `admin`, `config`.

- Subject in imperative mood, lowercase after the colon, no trailing period,
  under 72 characters.
- One logical change per commit.
- Body only when the *why* is not obvious from the subject. Wrap at 72 columns.
- Breaking changes get `!` before the colon and a `BREAKING CHANGE:` footer.
- No emoji.

Commit at every meaningful checkpoint, not once per phase. Before each commit
run `gofmt -l .`, `go vet ./...`, and `go test ./...`. Do not commit red.

## Code style

Standard Go, `gofmt` with no exceptions. Wrap errors with `%w` and enough
context to locate the failure. Log with `log/slog`, structured; no leftover
`fmt.Println` debugging. Table-driven tests. Keep functions short enough to read
on one screen.

## Storage rule

`objects.raw` stores the client's bytes verbatim. Never regenerate a payload
from a parsed model on read: clients put custom `X-` properties in there, and
round-tripping through our own serializer would drop them and churn ETags.
Index columns are derived on write and exist purely for querying.

## Design decisions

If a decision has real trade-offs, stop and ask rather than picking one and
building on it.
