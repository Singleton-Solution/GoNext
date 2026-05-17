;; the_content.wat — fixture plugin for the hook handler ABI.
;;
;; Exports:
;;   memory                           — 1 page, also imported by host
;;   gn_alloc(size i32) -> i32        — bump allocator, returns 0 on OOM
;;   gn_free(ptr i32, size i32)       — no-op (bump allocator doesn't free)
;;   gn_handle_hook(...) -> i64       — entry point per the ABI
;;   force_oom() -> i32               — test helper: exhaust the bump arena
;;
;; Behavior:
;;
;;   For hook name "the_content" with a FilterPayload whose value is a
;;   JSON string of ASCII, the guest produces a FilterResult whose
;;   value is the same string upper-cased.
;;
;;   For any other hook name, the guest returns ResultStatusUnknownHook
;;   (packed as (0, -4)).
;;
;;   For the action hook "on_event", the guest returns ResultStatusOK
;;   (packed as (0, 0)) — used to test the no-payload-result action
;;   path. It does no parsing; even an empty payload is fine.
;;
;;   For the action hook "trip_panic", the guest calls gn_panic with a
;;   short message — used to test trap propagation.
;;
;; Memory layout:
;;
;;   [0..4)         — current bump pointer (i32). Initial value 1024.
;;                    We reserve [0..1024) for fixed-purpose scratch:
;;                      [0..16)        — bump pointer + spare
;;                      [16..32)       — "the_content" name (11 bytes)
;;                      [32..48)       — "on_event"    name (8 bytes)
;;                      [48..64)       — "trip_panic"  name (10 bytes)
;;                      [64..80)       — "boom"        panic msg (4 bytes)
;;                      [80..96)       — output prefix "{\"value\":\""
;;                      [96..112)      — output suffix "\"}"
;;                      [128..136)     — search needle "\"value\":\""
;;   [1024..)       — bump-allocated region

(module
  (import "env" "gn_panic" (func $gn_panic (param i32 i32)))

  (memory (export "memory") 1)

  ;; Bump pointer initialized to 1024 (LEB128 fits in 2 bytes).
  (data (i32.const 0) "\00\04\00\00")

  ;; Fixed-purpose string scratch.
  (data (i32.const 16) "the_content")      ;; 11 bytes
  (data (i32.const 32) "on_event")          ;; 8 bytes
  (data (i32.const 48) "trip_panic")        ;; 10 bytes
  (data (i32.const 64) "boom")              ;; 4 bytes
  (data (i32.const 80) "{\"value\":\"")     ;; 10 bytes
  (data (i32.const 96) "\"}")               ;; 2 bytes
  (data (i32.const 128) "\"value\":\"")     ;; 9 bytes — search needle inside payload

  ;; ----- gn_alloc(size) -> ptr -------------------------------------
  ;;
  ;; Bump allocator. The bump pointer lives at memory[0..4]. Each call
  ;; returns the current pointer and advances it by `size` (rounded up
  ;; to 8 for cheap alignment). If the new pointer exceeds the current
  ;; memory size, returns 0 (OOM) — the host treats that as
  ;; ResultStatusOutOfMemory.
  (func $alloc (export "gn_alloc") (param $size i32) (result i32)
    (local $ptr i32)
    (local $next i32)
    (local $size_aligned i32)
    (local $mem_bytes i32)

    ;; size_aligned = (size + 7) & ~7
    (local.set $size_aligned
      (i32.and
        (i32.add (local.get $size) (i32.const 7))
        (i32.const -8)))

    ;; ptr = *(i32*)0
    (local.set $ptr (i32.load (i32.const 0)))
    ;; next = ptr + size_aligned
    (local.set $next (i32.add (local.get $ptr) (local.get $size_aligned)))
    ;; mem_bytes = memory.size * 65536
    (local.set $mem_bytes (i32.shl (memory.size) (i32.const 16)))

    ;; If next > mem_bytes, return 0 (OOM).
    (if (i32.gt_u (local.get $next) (local.get $mem_bytes))
      (then (return (i32.const 0))))

    ;; *(i32*)0 = next
    (i32.store (i32.const 0) (local.get $next))
    (local.get $ptr))

  ;; ----- gn_free(ptr, size) -> () ----------------------------------
  ;;
  ;; No-op: the bump allocator never frees individual slots.
  (func $free (export "gn_free") (param i32 i32))

  ;; ----- force_oom() -> i32 ----------------------------------------
  ;;
  ;; Test helper: pin the bump pointer to a value past memory end so
  ;; the next gn_alloc returns 0. Returns the bump pointer so tests
  ;; can verify.
  (func $force_oom (export "force_oom") (result i32)
    (i32.store (i32.const 0) (i32.const 0xFFFFFF00))
    (i32.load (i32.const 0)))

  ;; ----- memeq(a, b, n) -> i32 -------------------------------------
  ;;
  ;; Returns 1 if memory[a..a+n] == memory[b..b+n], else 0.
  (func $memeq (param $a i32) (param $b i32) (param $n i32) (result i32)
    (local $i i32)
    (local.set $i (i32.const 0))
    (block $done
      (loop $lp
        (br_if $done (i32.ge_u (local.get $i) (local.get $n)))
        (br_if $done
          (i32.ne
            (i32.load8_u (i32.add (local.get $a) (local.get $i)))
            (i32.load8_u (i32.add (local.get $b) (local.get $i)))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $lp)))
    ;; Equal iff we walked the whole range.
    (i32.eq (local.get $i) (local.get $n)))

  ;; ----- find_needle(haystack, hlen, needle, nlen) -> i32 ----------
  ;;
  ;; Returns the offset OF THE END of the needle inside haystack, or
  ;; -1 if not found. We return end-offset so the caller can step
  ;; directly to the byte after the needle.
  (func $find_needle
    (param $hay i32) (param $hlen i32)
    (param $needle i32) (param $nlen i32) (result i32)
    (local $i i32)
    (local $end i32)
    (local.set $i (i32.const 0))
    (local.set $end (i32.sub (local.get $hlen) (local.get $nlen)))
    (block $done
      (loop $lp
        (br_if $done (i32.gt_s (local.get $i) (local.get $end)))
        (if (call $memeq
              (i32.add (local.get $hay) (local.get $i))
              (local.get $needle)
              (local.get $nlen))
          (then
            (return (i32.add (local.get $i) (local.get $nlen)))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $lp)))
    (i32.const -1))

  ;; ----- pack_result(ptr, len) -> i64 ------------------------------
  ;;
  ;; (ptr as u32) << 32 | (len as u32). Used to build the gn_handle_hook
  ;; return value.
  (func $pack_result (param $ptr i32) (param $len i32) (result i64)
    (i64.or
      (i64.shl (i64.extend_i32_u (local.get $ptr)) (i64.const 32))
      (i64.extend_i32_u (local.get $len))))

  ;; ----- gn_handle_hook --------------------------------------------
  ;;
  ;; Dispatch on the hook name:
  ;;   "the_content" → uppercase filter
  ;;   "on_event"    → OK no-body
  ;;   "trip_panic"  → call gn_panic
  ;;   otherwise     → ResultStatusUnknownHook (-4)
  (func $gn_handle_hook (export "gn_handle_hook")
    (param $name_ptr i32) (param $name_len i32)
    (param $payload_ptr i32) (param $payload_len i32)
    (result i64)

    (local $val_start i32)        ;; offset of byte AFTER "value":"
    (local $val_end i32)          ;; offset of closing quote
    (local $i i32)
    (local $b i32)
    (local $out_ptr i32)          ;; bump-allocated output buffer
    (local $out_cursor i32)
    (local $val_len i32)
    (local $total_len i32)

    ;; --- Dispatch on name ---

    ;; on_event (length 8) — return OK no-body
    (if (i32.eq (local.get $name_len) (i32.const 8))
      (then
        (if (call $memeq (local.get $name_ptr) (i32.const 32) (i32.const 8))
          (then (return (call $pack_result (i32.const 0) (i32.const 0)))))))

    ;; trip_panic (length 10) — call gn_panic
    (if (i32.eq (local.get $name_len) (i32.const 10))
      (then
        (if (call $memeq (local.get $name_ptr) (i32.const 48) (i32.const 10))
          (then
            (call $gn_panic (i32.const 64) (i32.const 4))
            ;; gn_panic does not return; this is unreachable but the
            ;; verifier wants a value-producing path.
            (return (call $pack_result (i32.const 0) (i32.const 0)))))))

    ;; the_content (length 11) — uppercase filter
    (if (i32.eq (local.get $name_len) (i32.const 11))
      (then
        (if (call $memeq (local.get $name_ptr) (i32.const 16) (i32.const 11))
          (then
            ;; Find "\"value\":\"" in the payload. Needle is at offset 128, length 9.
            (local.set $val_start
              (call $find_needle
                (local.get $payload_ptr) (local.get $payload_len)
                (i32.const 128) (i32.const 9)))
            (if (i32.lt_s (local.get $val_start) (i32.const 0))
              (then
                ;; needle missing → bad payload
                (return (call $pack_result (i32.const 0) (i32.const -3)))))

            ;; Translate val_start (offset within payload) to absolute
            ;; memory addr.
            (local.set $val_start (i32.add (local.get $payload_ptr) (local.get $val_start)))

            ;; Find the closing quote. Scan from val_start until we hit
            ;; '"' (0x22). Bound by payload end.
            (local.set $val_end (local.get $val_start))
            (block $end_loop
              (loop $find_close
                (br_if $end_loop
                  (i32.ge_u (local.get $val_end)
                    (i32.add (local.get $payload_ptr) (local.get $payload_len))))
                (br_if $end_loop
                  (i32.eq (i32.load8_u (local.get $val_end)) (i32.const 0x22)))
                (local.set $val_end (i32.add (local.get $val_end) (i32.const 1)))
                (br $find_close)))

            (local.set $val_len (i32.sub (local.get $val_end) (local.get $val_start)))

            ;; Total length = 10 (prefix) + val_len + 2 (suffix)
            (local.set $total_len
              (i32.add (i32.const 12) (local.get $val_len)))

            ;; Allocate output buffer.
            (local.set $out_ptr (call $alloc (local.get $total_len)))
            (if (i32.eqz (local.get $out_ptr))
              (then
                (return (call $pack_result (i32.const 0) (i32.const -2)))))

            ;; Copy prefix.
            (local.set $i (i32.const 0))
            (local.set $out_cursor (local.get $out_ptr))
            (block $copy_pfx_done
              (loop $copy_pfx
                (br_if $copy_pfx_done (i32.ge_u (local.get $i) (i32.const 10)))
                (i32.store8
                  (local.get $out_cursor)
                  (i32.load8_u (i32.add (i32.const 80) (local.get $i))))
                (local.set $i (i32.add (local.get $i) (i32.const 1)))
                (local.set $out_cursor (i32.add (local.get $out_cursor) (i32.const 1)))
                (br $copy_pfx)))

            ;; Copy upper-cased value bytes.
            (local.set $i (i32.const 0))
            (block $copy_val_done
              (loop $copy_val
                (br_if $copy_val_done (i32.ge_u (local.get $i) (local.get $val_len)))
                (local.set $b (i32.load8_u (i32.add (local.get $val_start) (local.get $i))))
                ;; If b in [a..z] (0x61..0x7a), subtract 0x20.
                (if (i32.and
                      (i32.ge_u (local.get $b) (i32.const 0x61))
                      (i32.le_u (local.get $b) (i32.const 0x7a)))
                  (then (local.set $b (i32.sub (local.get $b) (i32.const 0x20)))))
                (i32.store8 (local.get $out_cursor) (local.get $b))
                (local.set $i (i32.add (local.get $i) (i32.const 1)))
                (local.set $out_cursor (i32.add (local.get $out_cursor) (i32.const 1)))
                (br $copy_val)))

            ;; Copy suffix.
            (local.set $i (i32.const 0))
            (block $copy_sfx_done
              (loop $copy_sfx
                (br_if $copy_sfx_done (i32.ge_u (local.get $i) (i32.const 2)))
                (i32.store8
                  (local.get $out_cursor)
                  (i32.load8_u (i32.add (i32.const 96) (local.get $i))))
                (local.set $i (i32.add (local.get $i) (i32.const 1)))
                (local.set $out_cursor (i32.add (local.get $out_cursor) (i32.const 1)))
                (br $copy_sfx)))

            (return (call $pack_result (local.get $out_ptr) (local.get $total_len)))))))

    ;; Default: unknown hook.
    (call $pack_result (i32.const 0) (i32.const -4))))
