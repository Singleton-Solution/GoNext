/**
 * Codec round-trip tests for the JSON envelope used by the GoNext
 * plugin ABI. The Go side's golden is
 * `packages/go/plugins/abi/hooks/marshal.go`; we cross-check the
 * encoded shapes match what `MarshalActionPayload` /
 * `MarshalFilterPayload` produce.
 */

import { describe, expect, it } from 'vitest';

import {
  CodecError,
  PayloadKind,
  ResultStatus,
  marshalActionPayload,
  marshalFilterPayload,
  marshalFilterResult,
  unmarshalActionPayload,
  unmarshalFilterPayload,
} from '../src/codec.ts';

describe('marshalActionPayload', () => {
  it('encodes an empty action payload', () => {
    expect(marshalActionPayload()).toBe('{"kind":"action","args":[]}');
  });

  it('normalises null args to an empty array', () => {
    expect(marshalActionPayload(null)).toBe('{"kind":"action","args":[]}');
  });

  it('preserves primitive args in order', () => {
    expect(marshalActionPayload([1, 'two', true, null])).toBe(
      '{"kind":"action","args":[1,"two",true,null]}',
    );
  });

  it('round-trips through unmarshalActionPayload', () => {
    const args = [{ post_id: 42 }, ['tag-a', 'tag-b']];
    const wire = marshalActionPayload(args);
    const decoded = unmarshalActionPayload(wire);
    expect(decoded.kind).toBe(PayloadKind.Action);
    expect(decoded.args).toEqual(args);
  });
});

describe('marshalFilterPayload', () => {
  it('emits an explicit null when value is undefined', () => {
    expect(marshalFilterPayload(undefined)).toBe(
      '{"kind":"filter","value":null,"args":[]}',
    );
  });

  it('treats null value as a legitimate sentinel', () => {
    expect(marshalFilterPayload(null)).toBe(
      '{"kind":"filter","value":null,"args":[]}',
    );
  });

  it('encodes a complex value and extras', () => {
    const wire = marshalFilterPayload(
      { post_id: 7, body: 'hello' },
      ['ctx-a', 'ctx-b'],
    );
    const decoded = unmarshalFilterPayload(wire);
    expect(decoded.kind).toBe(PayloadKind.Filter);
    expect(decoded.value).toEqual({ post_id: 7, body: 'hello' });
    expect(decoded.args).toEqual(['ctx-a', 'ctx-b']);
  });

  it('round-trips an array value', () => {
    const wire = marshalFilterPayload([1, 2, 3], null);
    const decoded = unmarshalFilterPayload(wire);
    expect(decoded.value).toEqual([1, 2, 3]);
    expect(decoded.args).toEqual([]);
  });
});

describe('marshalFilterResult', () => {
  it('emits null when undefined', () => {
    expect(marshalFilterResult(undefined)).toBe('{"value":null}');
  });

  it('emits the value verbatim', () => {
    expect(marshalFilterResult({ ok: true })).toBe('{"value":{"ok":true}}');
  });

  it('preserves strings', () => {
    expect(marshalFilterResult('hello')).toBe('{"value":"hello"}');
  });
});

describe('unmarshalActionPayload — error paths', () => {
  it('rejects malformed JSON with CodecError(BadPayload)', () => {
    expect(() => unmarshalActionPayload('{nope')).toThrow(CodecError);
    try {
      unmarshalActionPayload('{nope');
    } catch (err) {
      expect((err as CodecError).status).toBe(ResultStatus.BadPayload);
    }
  });

  it('rejects payloads with the wrong kind', () => {
    const wire = marshalFilterPayload(null);
    expect(() => unmarshalActionPayload(wire)).toThrow(CodecError);
  });

  it('rejects payloads with non-array args', () => {
    const wire = '{"kind":"action","args":"not-an-array"}';
    expect(() => unmarshalActionPayload(wire)).toThrow(CodecError);
  });

  it('rejects non-object roots', () => {
    expect(() => unmarshalActionPayload('null')).toThrow(CodecError);
    expect(() => unmarshalActionPayload('[]')).toThrow(CodecError);
  });
});

describe('unmarshalFilterPayload — error paths', () => {
  it('rejects malformed JSON', () => {
    expect(() => unmarshalFilterPayload('not json')).toThrow(CodecError);
  });

  it('rejects payloads missing the value field', () => {
    const wire = '{"kind":"filter","args":[]}';
    expect(() => unmarshalFilterPayload(wire)).toThrow(CodecError);
  });

  it('rejects payloads with wrong kind', () => {
    const wire = marshalActionPayload([]);
    expect(() => unmarshalFilterPayload(wire)).toThrow(CodecError);
  });

  it('rejects payloads with non-array args', () => {
    const wire = '{"kind":"filter","value":null,"args":42}';
    expect(() => unmarshalFilterPayload(wire)).toThrow(CodecError);
  });
});

describe('ResultStatus sentinels', () => {
  it('matches the Go abi/hooks ResultStatus constants', () => {
    expect(ResultStatus.OK).toBe(0);
    expect(ResultStatus.Error).toBe(-1);
    expect(ResultStatus.OutOfMemory).toBe(-2);
    expect(ResultStatus.BadPayload).toBe(-3);
    expect(ResultStatus.UnknownHook).toBe(-4);
    expect(ResultStatus.Trap).toBe(-5);
  });
});
