# Project history notes

This file records non-obvious facts about the git history that a future archaeologist (or contributor doing `git blame`) might find confusing. It is not a changelog (see [`CHANGELOG.md`](./CHANGELOG.md) when that exists) and not a roadmap (see [`ROADMAP.md`](./ROADMAP.md)). It's narrow: where the recorded commit history doesn't match the obvious story.

## 2026-05-17 — Commit `1feecc4` contains far more than its title suggests

**TL;DR.** The commit titled *"docs(contributing): add policy on unsolicited AI-generated content (#280)"* (SHA `1feecc4`) actually introduces the entire bootstrap monorepo skeleton, the structured logger, the typed env config loader, and the HTTP server chassis — in addition to the spam-policy change its title describes.

### What happened

The first wave of implementation was developed as a four-PR stack:

| Issue | Subsystem | Original PR |
|---|---|---|
| [#1](https://github.com/Singleton-Solution/GoNext/issues/1) | Monorepo bootstrap (Go + pnpm workspaces) | [#276](https://github.com/Singleton-Solution/GoNext/pull/276) |
| [#145](https://github.com/Singleton-Solution/GoNext/issues/145) | `packages/go/log` — structured logger with redaction | [#277](https://github.com/Singleton-Solution/GoNext/pull/277) |
| [#10](https://github.com/Singleton-Solution/GoNext/issues/10) | `packages/go/config` — typed env loader | [#278](https://github.com/Singleton-Solution/GoNext/pull/278) |
| [#2](https://github.com/Singleton-Solution/GoNext/issues/2) | `packages/go/httpx` + `apps/api/cmd/server` | [#279](https://github.com/Singleton-Solution/GoNext/pull/279) |

Each PR was stacked on the previous: #277's base was the head of #276, #278's base was the head of #277, #279's base was the head of #278.

Mid-stack, an unrelated docs-only PR [#280](https://github.com/Singleton-Solution/GoNext/pull/280) was opened to add the AI content policy to `CONTRIBUTING.md`. It was branched from the head of #279 — but its base was set to `main` rather than `feat/02-http-server`. GitHub computed the PR diff as *everything between `main` and the spam-policy branch*, which included all four upstream PRs' changes plus the single-file `CONTRIBUTING.md` addition.

When #280 was squash-merged into `main`, the cumulative diff collapsed into one commit (`1feecc4`) titled after #280's purpose, even though its contents are substantively the work of #1, #145, #10, and #2. PRs #276–#279 became stale duplicates and were closed as already-merged.

### What this means in practice

- **For `git blame`**: any line in `apps/api/`, `packages/go/buildinfo/`, `packages/go/log/`, `packages/go/config/`, `packages/go/httpx/`, `apps/web/`, `apps/admin/`, `packages/ts/*/`, `cli/gonext/`, `apps/worker/`, the top-level `Makefile`, `docker-compose.yml`, `.env.example`, `.editorconfig`, `.nvmrc`, `.tool-versions`, `.markdownlint.jsonc`, `migrations/README.md`, `plugins/README.md`, `themes/README.md`, `tools/README.md`, or `pnpm-workspace.yaml` will blame to `1feecc4`. The commit message will say "spam policy" but the real provenance is the four issues listed above.

- **For changelog generation**: a tool that walks `main`'s commits will see only one entry where there should be five. Hand-write the v0.0.x changelog entries from the issue list rather than from `git log`.

- **For licensing/CLA archaeology**: every change in `1feecc4` carries my `Signed-off-by:` trailer in the squashed commits; the original branch history is preserved in the closed PRs' refs (browse from each PR's page on GitHub) for as long as GitHub retains them.

### Why we did not redo the history

Two options were considered:

1. **Revert `1feecc4` and re-merge the four PRs in order.** Would produce clean history. Rejected because (a) the code in `1feecc4` is correct, tested, and smoke-verified; reverting and re-merging is mostly bookkeeping; (b) it would require force-pushing during a window when other work could be in flight; (c) the closed PRs still exist on GitHub as a reference if anyone needs the original commit sequence.

2. **Accept the misnamed squash + document it here.** Chosen. This file is the documentation.

### Lessons captured

- A stacked PR's `--base` flag must point at the PR's actual parent branch, not at `main`. Otherwise the cumulative diff becomes the PR's diff and a squash-merge collapses the whole stack.
- Branch protection on `main` (requiring review) would have caught this at merge time. It's planned but not yet enabled.

## Reading this file

Add new entries to the **top** of this file in reverse-chronological order. Each entry:
- Dates from the commit that fixed or recorded the issue.
- Names the SHA(s) involved.
- States the symptom (what someone confused would see).
- States the cause (what actually happened).
- States the resolution (what was done, or — for "we live with it" entries — what to know).

This file is intentionally narrow. Routine commits do not earn an entry here. Only commits that diverge from their visible metadata, history rewrites, or unusual merges that future contributors would otherwise have to reverse-engineer.
