//go:build !race

package wxr

// raceEnabled mirrors the build's -race flag. The memory threshold
// test skips itself when this is true because race instrumentation
// inflates HeapAlloc by 5-10x.
const raceEnabled = false
