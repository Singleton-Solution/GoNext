;; tightloop.wat — CPU-deadline test fixture.
;;
;; Exports `spin` which enters a `loop` that never breaks (the `br 0`
;; jumps back to the loop label unconditionally). With wazero
;; constructed with WithCloseOnContextDone(true), a call to `spin`
;; runs until the call context is cancelled — at which point wazero
;; traps the guest and Module.Call returns an error.
;;
;; Used by the limits package's CPU-timeout tests: arm a short
;; deadline, call spin, expect the call to return promptly with
;; a trap / DeadlineExceeded.
(module
  (memory (export "memory") 1)
  (func $spin (export "spin")
    (loop $forever
      br $forever)))
