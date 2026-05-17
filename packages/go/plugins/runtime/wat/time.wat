;; time.wat — gn_time_ms test fixture.
;;
;; Imports env.gn_time_ms and exports get_time() -> i64 that simply
;; calls the host and returns the result. Used to verify the time
;; host function is wired and the value passes through unchanged.
(module
  (import "env" "gn_time_ms" (func $gn_time_ms (result i64)))
  (func $get_time (export "get_time") (result i64)
    call $gn_time_ms))
