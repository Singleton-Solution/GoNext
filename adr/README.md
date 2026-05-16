# Architecture Decision Records (ADRs)

This directory contains ADRs — short, immutable records of significant architectural decisions.

## What's an ADR?

A markdown file capturing one decision. Format inspired by Michael Nygard's [Documenting Architecture Decisions](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions) post and the [adr-tools](https://github.com/npryce/adr-tools) project.

## When to write one

Write an ADR when:
- You make a decision that's hard to reverse later.
- You introduce a new core abstraction or external dependency.
- You change a public API, plugin ABI, theme contract, REST/GraphQL schema, or DB schema in a breaking way.
- You resolve a long-running architectural debate.
- You accept a known tradeoff or limitation.

Don't write one for:
- Implementation choices internal to a package.
- Style decisions (those live in lint config).
- Reversible product decisions (those live in issues or discussions).

## Format

See [`0000-template.md`](./0000-template.md).

Filename: `NNNN-short-kebab-case-title.md` where `NNNN` is a zero-padded sequence number.

## Status lifecycle

```
proposed → accepted → (superseded by NNNN | deprecated)
```

- **proposed**: Drafted but not yet ratified. Open as a `design-discussion` issue first; ADR PR after consensus.
- **accepted**: Maintainers have approved. This is now binding.
- **superseded**: A later ADR replaces this one. The replacement names the predecessor.
- **deprecated**: No longer relevant, not yet replaced.

ADRs are **immutable once accepted**. To change a decision, write a new ADR that supersedes the old one.

## Index

(Generated as ADRs land. See `/adr/*.md`.)

| #    | Title | Status |
|------|---|---|
| 0000 | Template | (template) |
| 0001 | Licensing — source-available core, permissive SDK | proposed |
| 0002 | DCO requirement (CLA deferred) | proposed |
| 0003 | UUID v7 primary keys | proposed |
| 0004 | Postgres as primary store | proposed |
| 0005 | WebAssembly plugin runtime via wazero | proposed |
| 0006 | Monorepo with Go + pnpm workspaces | proposed |
| 0007 | Separate Next.js apps for public + admin | proposed |
| 0008 | JSON block tree storage | proposed |
| 0009 | Lexical for rich text | proposed |
| 0010 | Asynq for background jobs | proposed |
| 0011 | Tag-based cache invalidation via transactional outbox | proposed |
| 0012 | Plugin capability ABI | proposed |
