;; panic.wat — guest-trap test fixture.
;;
;; Imports env.gn_panic and exports boom() which immediately calls
;; gn_panic with the pre-laid-out message "boom from guest" at offset
;; 0 in the module's linear memory. Used to verify TrapError carries
;; the guest message back to the caller.
(module
  (import "env" "gn_panic" (func $gn_panic (param i32 i32)))
  (memory (export "memory") 1)
  (data (i32.const 0) "boom from guest")
  (func $boom (export "boom")
    i32.const 0   ;; ptr — start of the string
    i32.const 15  ;; len — bytes in "boom from guest"
    call $gn_panic))
