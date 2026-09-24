//go:build codemode && race

package codemode

// raceMemoryMB is added to the sandbox's memory limit in tests: the race
// detector's shadow memory counts against RLIMIT_AS.
const raceMemoryMB = 256

// raceEnabled: the race detector makes native code several times
// slower, so a slow allocation can hit the CPU limit before the memory one.
const raceEnabled = true
