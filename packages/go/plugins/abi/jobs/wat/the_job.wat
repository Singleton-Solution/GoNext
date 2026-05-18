;; the_job.wat — fixture plugin for the job handler ABI.
;;
;; Exports:
;;   memory                           — 1 page, also imported by host
;;   gn_alloc(size i32) -> i32        — bump allocator, returns 0 on OOM
;;   gn_free(ptr i32, size i32)       — no-op (bump allocator doesn't free)
;;   gn_handle_job(...) -> i64        — entry point per the ABI
;;   force_oom() -> i32               — test helper: exhaust the bump arena
;;
;; Behavior:
;;
;;   For job name "email.send", the guest returns ResultStatusOK
;;   (packed as (0, 0)) — used to test the happy-path job dispatch.
;;   It does no parsing; the envelope's idempotency_key, retry_count
;;   and payload are accepted verbatim.
;;
;;   For job name "trap.job", the guest calls gn_panic with a short
;;   message — used to test trap propagation through the job bridge.
;;
;;   For any other job name, the guest returns ResultStatusUnknownJob
;;   (packed as (0, -4)).
;;
;; Memory layout:
;;
;;   [0..4)         — current bump pointer (i32). Initial value 1024.
;;                    We reserve [0..1024) for fixed-purpose scratch:
;;                      [0..16)        — bump pointer + spare
;;                      [16..32)       — "email.send" name (10 bytes)
;;                      [32..48)       — "trap.job"   name (8 bytes)
;;                      [48..64)       — "boom"       panic msg (4 bytes)
;;   [1024..)       — bump-allocated region

(module
  (import "env" "gn_panic" (func $gn_panic (param i32 i32)))

  (memory (export "memory") 1)

  ;; Bump pointer initialized to 1024 (LEB128 fits in 2 bytes).
  (data (i32.const 0) "\00\04\00\00")

  ;; Fixed-purpose string scratch.
  (data (i32.const 16) "email.send")      ;; 10 bytes
  (data (i32.const 32) "trap.job")         ;; 8 bytes
  (data (i32.const 48) "boom")             ;; 4 bytes

  ;; ----- gn_alloc(size) -> ptr -------------------------------------
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

    (local.set $ptr (i32.load (i32.const 0)))
    (local.set $next (i32.add (local.get $ptr) (local.get $size_aligned)))
    (local.set $mem_bytes (i32.shl (memory.size) (i32.const 16)))

    (if (i32.gt_u (local.get $next) (local.get $mem_bytes))
      (then (return (i32.const 0))))

    (i32.store (i32.const 0) (local.get $next))
    (local.get $ptr))

  ;; ----- gn_free(ptr, size) -> () ----------------------------------
  (func $free (export "gn_free") (param i32 i32))

  ;; ----- force_oom() -> i32 ----------------------------------------
  (func $force_oom (export "force_oom") (result i32)
    (i32.store (i32.const 0) (i32.const 0xFFFFFF00))
    (i32.load (i32.const 0)))

  ;; ----- memeq(a, b, n) -> i32 -------------------------------------
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
    (i32.eq (local.get $i) (local.get $n)))

  ;; ----- pack_result(ptr, len) -> i64 ------------------------------
  (func $pack_result (param $ptr i32) (param $len i32) (result i64)
    (i64.or
      (i64.shl (i64.extend_i32_u (local.get $ptr)) (i64.const 32))
      (i64.extend_i32_u (local.get $len))))

  ;; ----- gn_handle_job ---------------------------------------------
  ;;
  ;; Dispatch on the job name:
  ;;   "email.send" (10)  → OK no-body
  ;;   "trap.job"   (8)   → call gn_panic
  ;;   otherwise          → ResultStatusUnknownJob (-4)
  (func $gn_handle_job (export "gn_handle_job")
    (param $name_ptr i32) (param $name_len i32)
    (param $payload_ptr i32) (param $payload_len i32)
    (result i64)

    ;; email.send (length 10) — return OK no-body
    (if (i32.eq (local.get $name_len) (i32.const 10))
      (then
        (if (call $memeq (local.get $name_ptr) (i32.const 16) (i32.const 10))
          (then (return (call $pack_result (i32.const 0) (i32.const 0)))))))

    ;; trap.job (length 8) — call gn_panic
    (if (i32.eq (local.get $name_len) (i32.const 8))
      (then
        (if (call $memeq (local.get $name_ptr) (i32.const 32) (i32.const 8))
          (then
            (call $gn_panic (i32.const 48) (i32.const 4))
            ;; gn_panic does not return; verifier wants a value-path.
            (return (call $pack_result (i32.const 0) (i32.const 0)))))))

    ;; Default: unknown job.
    (call $pack_result (i32.const 0) (i32.const -4))))
