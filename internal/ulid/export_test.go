package ulid

// SetReadRandom swaps the package's randomness source for tests and returns a
// function that restores the previous source. It exists only in the test build
// so the panic path in [New] can be exercised without touching the public API.
func SetReadRandom(fn func([]byte) (int, error)) (restore func()) {
	prev := readRandom
	readRandom = fn
	return func() { readRandom = prev }
}
