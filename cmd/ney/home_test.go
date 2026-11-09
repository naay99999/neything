package main

import "testing"

// setHome points the user's home directory at dir for the test. Windows'
// os.UserHomeDir reads USERPROFILE, not HOME, so both are set.
func setHome(tb testing.TB, dir string) {
	tb.Helper()
	tb.Setenv("HOME", dir)
	tb.Setenv("USERPROFILE", dir)
}
