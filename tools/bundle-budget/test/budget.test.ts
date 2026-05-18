/**
 * Tests for the budgets.yaml loader.
 */
import { describe, it, expect } from 'vitest';
import { parseBudgets, findRouteBudget } from '../src/budget.ts';

describe('parseBudgets', () => {
  it('parses a well-formed budget file', () => {
    const source = `
global:
  maxTransferKb: 1000
routes:
  - app: admin
    route: /
    maxJsKb: 250
    maxCssKb: 40
    maxFontKb: 100
  - app: web
    route: /
    maxJsKb: 80
    maxCssKb: 30
`;
    const budgets = parseBudgets(source);
    expect(budgets.global.maxTransferKb).toBe(1000);
    expect(budgets.routes).toHaveLength(2);
    expect(budgets.routes[0]).toMatchObject({
      app: 'admin',
      route: '/',
      maxJsKb: 250,
      maxCssKb: 40,
      maxFontKb: 100,
    });
    // maxFontKb is optional and is absent in the second route.
    expect(budgets.routes[1]?.maxFontKb).toBeUndefined();
  });

  it('throws on missing global block', () => {
    expect(() => parseBudgets('routes: []')).toThrow(/global/);
  });

  it('throws when global.maxTransferKb is not a number', () => {
    expect(() =>
      parseBudgets(`
global:
  maxTransferKb: "lots"
routes: []
`),
    ).toThrow(/maxTransferKb/);
  });

  it('throws when routes is not a list', () => {
    expect(() =>
      parseBudgets(`
global:
  maxTransferKb: 1000
routes:
  not: a-list
`),
    ).toThrow(/routes/);
  });

  it('throws when a route entry is missing a required field', () => {
    expect(() =>
      parseBudgets(`
global:
  maxTransferKb: 1000
routes:
  - app: admin
    route: /
    maxCssKb: 40
`),
    ).toThrow(/maxJsKb/);
  });

  it('throws on a non-object route entry', () => {
    expect(() =>
      parseBudgets(`
global:
  maxTransferKb: 1000
routes:
  - just-a-string
`),
    ).toThrow(/routes\[0\]/);
  });

  it('throws on empty input', () => {
    expect(() => parseBudgets('')).toThrow();
  });
});

describe('findRouteBudget', () => {
  const budgets = parseBudgets(`
global:
  maxTransferKb: 1000
routes:
  - app: admin
    route: /
    maxJsKb: 250
    maxCssKb: 40
  - app: admin
    route: /posts/[id]/edit
    maxJsKb: 600
    maxCssKb: 60
  - app: web
    route: /
    maxJsKb: 80
    maxCssKb: 30
`);

  it('finds an exact match', () => {
    const r = findRouteBudget(budgets, 'admin', '/');
    expect(r?.maxJsKb).toBe(250);
  });

  it('distinguishes by app', () => {
    const r = findRouteBudget(budgets, 'web', '/');
    expect(r?.maxJsKb).toBe(80);
  });

  it('matches dynamic segments verbatim', () => {
    const r = findRouteBudget(budgets, 'admin', '/posts/[id]/edit');
    expect(r?.maxJsKb).toBe(600);
  });

  it('returns undefined for an unknown route', () => {
    expect(findRouteBudget(budgets, 'admin', '/nope')).toBeUndefined();
  });

  it('returns undefined for an unknown app', () => {
    expect(findRouteBudget(budgets, 'nope', '/')).toBeUndefined();
  });
});
