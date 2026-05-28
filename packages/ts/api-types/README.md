# @gonext/api-types

TypeScript types generated from `apps/api/openapi/openapi.yaml` — the hand-authored source of truth for every GoNext REST endpoint. Consumers (`apps/admin`, `packages/ts/sdk`) import wire shapes from here so we stop hand-writing types that drift from the server.

## Usage

```ts
import type { paths, components } from '@gonext/api-types';

// Pick a response shape by path/method/status/content-type:
type PostListResponse =
  paths['/api/v1/posts']['get']['responses']['200']['content']['application/json'];

// Or pull a schema directly out of components:
type Post = components['schemas']['Post'];
```

## Regenerating

The generated output is committed at `src/generated.ts` so `pnpm install` doesn't need to run codegen. After editing `apps/api/openapi/openapi.yaml`, regenerate from this package:

```sh
pnpm --filter @gonext/api-types generate
```

That runs `openapi-typescript ../../../apps/api/openapi/openapi.yaml -o src/generated.ts`. Commit the resulting diff alongside the spec change so reviewers see both halves of the contract update.

## Why we commit the generated file

Two reasonable patterns exist — regenerate on every install, or commit the generated output. We commit because (a) `pnpm install` stays fast and side-effect-free, (b) PR diffs make spec changes auditable, and (c) consumers can typecheck immediately without a separate codegen step.
