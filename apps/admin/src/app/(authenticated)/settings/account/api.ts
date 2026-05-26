/**
 * /settings/account — API helpers for the passkey list (issue #159).
 *
 * The browser side of the WebAuthn ceremony uses
 * navigator.credentials.create / .get with options the API issues at
 * the /begin endpoint; this module just exposes thin fetch wrappers
 * over those four endpoints plus the list+delete admin surface.
 *
 * No retry / no caching layer — the call sites are user-driven (one
 * button-click per request) and a typed wrapper around `fetch` keeps
 * the surface predictable.
 */
import { api } from '@/lib/api-client';

export interface PasskeyView {
  id: string;
  name: string;
  created_at: string;
  last_used_at?: string;
}

/**
 * GET /api/v1/auth/webauthn/credentials — list the signed-in user's
 * registered passkeys. Returns the empty array on no rows.
 */
export async function listPasskeys(): Promise<PasskeyView[]> {
  const res = await api.get<{ data: PasskeyView[] }>(
    '/api/v1/auth/webauthn/credentials',
  );
  return res?.data ?? [];
}

/**
 * DELETE /api/v1/auth/webauthn/credentials/{id} — revoke a passkey.
 *
 * The API enforces row ownership; an attempt to delete another
 * user's row returns 404 (intentionally — the row's existence is
 * also a leak to anybody enumerating ids, so we mask it).
 */
export async function deletePasskey(id: string): Promise<void> {
  await api.delete<void>(
    `/api/v1/auth/webauthn/credentials/${encodeURIComponent(id)}`,
  );
}

/**
 * registerPasskey runs the full registration ceremony:
 *
 *   1. POST /register/begin — the server returns a ceremony id and a
 *      CredentialCreationOptions blob.
 *   2. navigator.credentials.create(options) — the browser prompts
 *      the user to choose an authenticator and produces an
 *      attestation response.
 *   3. POST /register/finish — the server validates the attestation
 *      and persists the credential.
 *
 * Returns the row id + the friendly name; the caller refreshes the
 * list.
 *
 * Errors:
 *   - User cancellation surfaces as DOMException("NotAllowedError")
 *     from credentials.create — we surface it as a typed error code
 *     "cancelled" so the UI can render a non-alarming message.
 *   - Server-side validation failure surfaces as the response's
 *     error payload, wrapped in an Error.
 */
export async function registerPasskey(
  name: string,
): Promise<{ id: string; name: string }> {
  const beginRes = await api.post<{
    ceremony_id: string;
    options: { publicKey: PublicKeyCredentialCreationOptionsJSON };
  }>('/api/v1/auth/webauthn/register/begin', {});

  if (typeof navigator === 'undefined' || !navigator.credentials) {
    throw new Error('WebAuthn not available in this browser');
  }

  // The API returns the options inside `options.publicKey` (matching
  // the spec's shape). We pass through the inner publicKey object to
  // the browser API. Some fields arrive base64-encoded (challenge,
  // user.id, excludeCredentials[].id) and need to be decoded into
  // ArrayBuffers before the browser will accept them.
  //
  // The cast through `unknown` is unavoidable: TypeScript's DOM lib
  // models the options as the post-decode shape with BufferSources,
  // but the wire shape is the pre-decode JSON. We patch up the three
  // base64-coded fields, then assert the result back into the spec
  // type — the runtime structure matches by construction.
  const opts = beginRes.options.publicKey;
  const publicKey = {
    ...opts,
    challenge: b64ToBuf(opts.challenge as unknown as string),
    user: {
      ...opts.user,
      id: b64ToBuf(opts.user.id as unknown as string),
    },
    excludeCredentials: (opts.excludeCredentials ?? []).map((c) => ({
      ...c,
      id: b64ToBuf(c.id as unknown as string),
    })),
  } as unknown as PublicKeyCredentialCreationOptions;

  let credential: PublicKeyCredential | null;
  try {
    credential = (await navigator.credentials.create({
      publicKey,
    })) as PublicKeyCredential | null;
  } catch (err) {
    if (err instanceof DOMException && err.name === 'NotAllowedError') {
      throw new Error('cancelled');
    }
    throw err;
  }
  if (!credential) {
    throw new Error('no_credential');
  }

  const attestation = serializeAttestation(credential);
  const qs = new URLSearchParams({
    ceremony_id: beginRes.ceremony_id,
    name,
  });
  return api.post<{ id: string; name: string }>(
    `/api/v1/auth/webauthn/register/finish?${qs.toString()}`,
    attestation,
  );
}

/**
 * The TypeScript DOM lib types CredentialCreationOptions's
 * `excludeCredentials[].id` as a BufferSource, but the API ships JSON
 * (base64url-encoded). This type captures the wire shape so we can
 * be explicit about the decode step above.
 */
interface PublicKeyCredentialCreationOptionsJSON {
  challenge: string;
  user: { id: string; name: string; displayName: string };
  excludeCredentials?: Array<{ id: string; type: 'public-key' }>;
  // Additional fields (rp, pubKeyCredParams, ...) pass through
  // unmodified — they're already JSON-friendly primitives.
  [key: string]: unknown;
}

/**
 * b64ToBuf decodes a base64url-encoded string into an ArrayBuffer the
 * Credentials API will accept. Browsers do NOT accept the standard
 * `+` / `/` alphabet here; we re-pad and translate before decoding.
 */
function b64ToBuf(s: string): ArrayBuffer {
  // base64url uses `-` / `_`; standard base64 uses `+` / `/`.
  const standard = s.replace(/-/g, '+').replace(/_/g, '/');
  // Pad to a multiple of 4 with `=`.
  const padded = standard + '='.repeat((4 - (standard.length % 4)) % 4);
  const bin = atob(padded);
  const buf = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i);
  return buf.buffer;
}

/**
 * bufToB64 is the inverse of b64ToBuf; produces a base64url-encoded
 * string (no padding) suitable for round-tripping through the API.
 */
function bufToB64(b: ArrayBuffer): string {
  const bytes = new Uint8Array(b);
  let s = '';
  for (let i = 0; i < bytes.byteLength; i++) {
    const ch = bytes[i] ?? 0;
    s += String.fromCharCode(ch);
  }
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/**
 * serializeAttestation converts the browser's PublicKeyCredential
 * into the JSON shape the API library expects. The library reads
 * the request body via r.Body and re-parses it; the field names
 * here match the WebAuthn-spec JSON encoding.
 */
function serializeAttestation(c: PublicKeyCredential): Record<string, unknown> {
  const resp = c.response as AuthenticatorAttestationResponse;
  return {
    id: c.id,
    rawId: bufToB64(c.rawId),
    type: c.type,
    response: {
      attestationObject: bufToB64(resp.attestationObject),
      clientDataJSON: bufToB64(resp.clientDataJSON),
    },
    clientExtensionResults: c.getClientExtensionResults?.() ?? {},
  };
}
