;; add.wat — minimal export test fixture.
;;
;; Exports a single function `add(i32, i32) -> i32` that returns the
;; sum of its two arguments. Used by the runtime tests to verify the
;; happy-path Call() interface: load bytes, look up export, invoke
;; with params, decode result.
(module
  (func $add (export "add") (param i32 i32) (result i32)
    local.get 0
    local.get 1
    i32.add))
