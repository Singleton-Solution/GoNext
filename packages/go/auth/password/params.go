package password

// Params controls the argon2id cost. The zero value is invalid; use
// DefaultParams or construct explicitly.
//
// Field meanings (RFC 9106):
//
//   - Memory: KiB of memory to allocate. RFC 9106 recommends 65536 (64 MiB)
//     for the "memory-constrained" profile; we use it as the floor.
//   - Iterations: number of passes over the memory. More passes = more CPU
//     time per attempt. 3 is the RFC 9106 default for the 64 MiB profile.
//   - Parallelism: number of lanes. Bound by available CPU cores at hash
//     time; verifiers must accept any value the producer chose.
//   - SaltLen: bytes of random salt. 16 is the standard; never go below.
//   - KeyLen: bytes of output hash. 32 is standard.
type Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLen     uint32
	KeyLen      uint32
}

// DefaultParams is the current production cost. RFC 9106 §4 "Recommended
// parameters" — memory-constrained profile.
//
// Bumping any of these fields will cause Verify to return needsRehash=true
// for all hashes produced under the previous defaults; the calling code
// is then expected to re-hash on next successful login.
var DefaultParams = Params{
	Memory:      64 * 1024, // 64 MiB
	Iterations:  3,
	Parallelism: 2,
	SaltLen:     16,
	KeyLen:      32,
}

// weakerThan reports whether p is weaker than other on any cost dimension.
// Used by Verify to decide needsRehash: if the stored params are weaker
// than the current default in any of memory/iterations/key-length, we
// want the caller to upgrade. Parallelism is intentionally excluded —
// it's a hardware-affinity parameter, not a security parameter.
func (p Params) weakerThan(other Params) bool {
	if p.Memory < other.Memory {
		return true
	}
	if p.Iterations < other.Iterations {
		return true
	}
	if p.KeyLen < other.KeyLen {
		return true
	}
	if p.SaltLen < other.SaltLen {
		return true
	}
	return false
}
