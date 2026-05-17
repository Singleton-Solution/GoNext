;; bigmem.wat — memory cap test fixture.
;;
;; Declares an initial memory of 1024 pages (= 64 MiB), which exceeds
;; the runtime's 256-page (16 MiB) hard cap. Instantiation must fail
;; cleanly with a *CompileError-wrapped wazero error, not a host panic.
(module
  (memory (export "memory") 1024)
  (func $touch (export "touch")))
