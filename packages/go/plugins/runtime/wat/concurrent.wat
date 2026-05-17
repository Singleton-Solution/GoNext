;; concurrent.wat — concurrent-call test fixture.
;;
;; Exports square(i32) -> i32 that returns x*x. Used by the race test
;; to fire N goroutines at the same module and verify serialization
;; produces the right answer for every call.
(module
  (func $square (export "square") (param i32) (result i32)
    local.get 0
    local.get 0
    i32.mul))
