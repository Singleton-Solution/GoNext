;; log.wat — host-log test fixture.
;;
;; Imports env.gn_log and exports say_hi() which logs "hi" at level 1
;; (info). Used to verify gn_log is reachable and routed to slog.
(module
  (import "env" "gn_log" (func $gn_log (param i32 i32 i32)))
  (memory (export "memory") 1)
  (data (i32.const 0) "hi from plugin")
  (func $say_hi (export "say_hi")
    i32.const 1   ;; level — info
    i32.const 0   ;; ptr
    i32.const 14  ;; len of "hi from plugin"
    call $gn_log))
