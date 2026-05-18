/**
 * Subresource Integrity (SRI) helpers for plugin-contributed script tags.
 *
 * SRI lets the browser refuse to execute a `<script src>` whose fetched
 * bytes don't match the integrity attribute. For plugin scripts in the
 * admin this means a compromised CDN or an attacker that intercepts the
 * plugin update channel cannot swap in malicious bytes without breaking
 * the integrity check first — the browser blocks execution before any
 * code runs.
 *
 * The spec (https://www.w3.org/TR/SRI/) defines three allowed hash algs:
 *
 *     sha256  sha384  sha512
 *
 * The integrity attribute serializes as `"<alg>-<base64>"`. Multiple
 * comma-separated hashes are allowed; this module emits exactly one so
 * the wire shape is unambiguous and tests can pin it.
 *
 * `sha384` is the recommended default — it offers comfortable collision
 * resistance, is supported in every browser that implements SRI, and is
 * what npm's lockfile uses for its own integrity hashes (so the
 * value can flow straight from package metadata to the host without
 * a re-hash step).
 *
 * This module is environment-agnostic: it uses Web Crypto when
 * `globalThis.crypto.subtle` is available (browser, Node 20+ globals,
 * Edge runtimes) and falls back to dynamically importing `node:crypto`
 * on older Node servers. The dynamic import is async so the public API
 * is Promise-based; callers are expected to await once at registration
 * time, not on every render.
 */

/** Supported SRI hash algorithms. Order matters: stronger first. */
export const SRI_ALGORITHMS = ['sha384', 'sha512', 'sha256'] as const;
export type SriAlgorithm = (typeof SRI_ALGORITHMS)[number];

/**
 * A parsed `integrity=` attribute. The original serialized form is kept
 * alongside the parts so `toString()` produces the canonical wire value
 * without re-serialization noise.
 */
export interface SriHash {
  /** Hash algorithm (e.g. `'sha384'`). */
  algorithm: SriAlgorithm;
  /** Base64-encoded digest WITHOUT the `<alg>-` prefix. */
  digestBase64: string;
  /** Full integrity attribute value, e.g. `'sha384-...'`. */
  attribute: string;
}

/**
 * Computes the SRI hash of `bytes` using the named algorithm and returns
 * the parsed result. Always uses the platform's strongest implementation
 * (WebCrypto when available, `node:crypto` otherwise).
 *
 * Input may be a `Uint8Array`, an `ArrayBuffer`, or a string. Strings
 * are encoded as UTF-8 before hashing — match this with
 * `Buffer.from(input, 'utf-8')` on the producer side or the digests
 * will not align.
 */
export async function computeSRI(
  bytes: Uint8Array | ArrayBuffer | string,
  algorithm: SriAlgorithm = 'sha384',
): Promise<SriHash> {
  assertAlgorithm(algorithm);
  const buffer = toUint8Array(bytes);
  const digest = await digestBytes(buffer, algorithm);
  const digestBase64 = base64Encode(digest);
  return {
    algorithm,
    digestBase64,
    attribute: `${algorithm}-${digestBase64}`,
  };
}

/**
 * Parses an `integrity=` attribute value into a structured `SriHash`.
 *
 * Throws when the attribute is malformed (missing algorithm, unknown
 * algorithm, or empty digest). Callers that want a tolerant flow should
 * wrap the call in a try/catch — the strictness is intentional so
 * misconfigured manifests fail loudly at registration time.
 *
 * The spec allows multiple comma-separated hashes; this parser accepts
 * the first usable token and ignores the rest.
 */
export function parseSRI(attribute: string): SriHash {
  const tokens = attribute.split(/\s+|,/).map((t) => t.trim()).filter(Boolean);
  for (const token of tokens) {
    const dash = token.indexOf('-');
    if (dash <= 0) {
      continue;
    }
    const alg = token.slice(0, dash);
    const digest = token.slice(dash + 1);
    if (!isSriAlgorithm(alg) || digest === '') {
      continue;
    }
    return { algorithm: alg, digestBase64: digest, attribute: token };
  }
  throw new TypeError(
    `[plugin-frontend-host] integrity attribute ${JSON.stringify(
      attribute,
    )} did not contain a recognized SRI hash.`,
  );
}

/**
 * Returns true iff `bytes` hashes to `expected`. Computes the actual
 * hash using the algorithm declared in `expected` so callers don't need
 * to thread it through separately. Uses a constant-time comparison on
 * the digest bytes to keep the function safe even when callers route
 * untrusted strings through it.
 */
export async function verifySRI(
  bytes: Uint8Array | ArrayBuffer | string,
  expected: string | SriHash,
): Promise<boolean> {
  const parsed = typeof expected === 'string' ? parseSRI(expected) : expected;
  const actual = await computeSRI(bytes, parsed.algorithm);
  return constantTimeStringEquals(actual.digestBase64, parsed.digestBase64);
}

/**
 * Validates that a string looks like a CSP / SRI hash attribute.
 * Useful for plugin manifest validators that want to reject malformed
 * integrity values without computing them.
 */
export function isValidSRIAttribute(value: string): boolean {
  try {
    parseSRI(value);
    return true;
  } catch {
    return false;
  }
}

/**
 * Asserts that an algorithm name is supported and throws otherwise.
 * Extracted so the error message stays uniform across entry points.
 */
function assertAlgorithm(algorithm: string): asserts algorithm is SriAlgorithm {
  if (!isSriAlgorithm(algorithm)) {
    throw new TypeError(
      `[plugin-frontend-host] unsupported SRI algorithm ${JSON.stringify(
        algorithm,
      )}; expected one of ${SRI_ALGORITHMS.join(', ')}.`,
    );
  }
}

/**
 * Type-narrowing predicate for SRI algorithm names.
 */
function isSriAlgorithm(value: string): value is SriAlgorithm {
  return (SRI_ALGORITHMS as ReadonlyArray<string>).includes(value);
}

/**
 * Normalizes the input bytes to a `Uint8Array` suitable for both the
 * WebCrypto and Node paths.
 */
function toUint8Array(bytes: Uint8Array | ArrayBuffer | string): Uint8Array {
  if (typeof bytes === 'string') {
    return new TextEncoder().encode(bytes);
  }
  if (bytes instanceof Uint8Array) {
    return bytes;
  }
  return new Uint8Array(bytes);
}

/**
 * Hashes `bytes` using the requested algorithm. Uses WebCrypto when
 * available; otherwise dynamically loads `node:crypto` so the package
 * stays importable in the browser without bundling the Node module.
 */
async function digestBytes(bytes: Uint8Array, algorithm: SriAlgorithm): Promise<Uint8Array> {
  const subtle = getSubtleCrypto();
  if (subtle !== null) {
    const wcName = algorithm === 'sha256' ? 'SHA-256' : algorithm === 'sha384' ? 'SHA-384' : 'SHA-512';
    const digest = await subtle.digest(wcName, bytes);
    return new Uint8Array(digest);
  }
  // Node fallback path — older runtimes without WebCrypto globals.
  // Use the dynamic spec import so bundlers tree-shake the Node-only
  // path away in the browser build.
  const nodeCrypto = await import('node:crypto');
  const hash = nodeCrypto.createHash(algorithm).update(bytes);
  const buf = hash.digest();
  return new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
}

/**
 * Reads `globalThis.crypto.subtle` defensively.
 */
function getSubtleCrypto(): { digest(alg: string, data: Uint8Array): Promise<ArrayBuffer> } | null {
  const c = (globalThis as { crypto?: { subtle?: unknown } }).crypto;
  if (c === undefined || c === null) {
    return null;
  }
  if (typeof (c.subtle as { digest?: unknown } | undefined)?.digest !== 'function') {
    return null;
  }
  return c.subtle as { digest(alg: string, data: Uint8Array): Promise<ArrayBuffer> };
}

/**
 * Base64-encodes a byte array. Uses `btoa` when available; falls back
 * to manual encoding so the function works in Node without `Buffer`
 * (which is convenient for Edge runtimes).
 */
function base64Encode(bytes: Uint8Array): string {
  if (typeof Buffer !== 'undefined' && typeof Buffer.from === 'function') {
    return Buffer.from(bytes).toString('base64');
  }
  let binary = '';
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]!);
  }
  return globalThis.btoa(binary);
}

/**
 * Constant-time string equality on ASCII-safe digests. Both inputs are
 * base64 strings so iterating their characters is safe.
 *
 * Implemented in TS rather than via WebCrypto's `timingSafeEqual` (Node
 * has it, browsers do not) so the function is portable. The branch on
 * length leaks "different length" — that's fine here because the
 * algorithm fixes the digest length and a mismatch there indicates a
 * misconfigured manifest, not a secret.
 */
function constantTimeStringEquals(a: string, b: string): boolean {
  if (a.length !== b.length) {
    return false;
  }
  let mismatch = 0;
  for (let i = 0; i < a.length; i++) {
    mismatch |= a.charCodeAt(i) ^ b.charCodeAt(i);
  }
  return mismatch === 0;
}
