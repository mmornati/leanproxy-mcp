//go:build codemode && !race

package codemode

// raceMemoryMB: see race_test.go.
const raceMemoryMB = 0

// raceEnabled: the race detector makes native code several times
// slower, so a slow allocation can hit the CPU limit before the memory one.
const raceEnabled = false
