// Runnable godoc examples for the ulid surface. Each Example function's
// // Output: block is checked by
// `go test -run "^Example" ./internal/ulid/...`. New is non-deterministic
// (timestamp + randomness), so ExampleNew asserts on the ID's shape rather
// than printing the value.
package ulid_test

import (
	"fmt"

	"kitsoki/internal/ulid"
)

// ExampleNew generates an ID and confirms the contract: a fresh ULID is
// exactly the fixed length and passes its own validity check.
func ExampleNew() {
	id := ulid.New()
	fmt.Println("length:", len(id))
	fmt.Println("valid: ", ulid.IsValid(id))
	// Output:
	// length: 26
	// valid:  true
}

// ExampleIsValid shows the shape contract: a canonical 26-char ULID is valid,
// the same value lower-cased is still valid (Crockford decodes
// case-insensitively), and length or alphabet violations are rejected.
func ExampleIsValid() {
	fmt.Println("canonical: ", ulid.IsValid("01ARZ3NDEKTSV4RRFFQ69G5FAV"))
	fmt.Println("lowercase: ", ulid.IsValid("01arz3ndektsv4rrffq69g5fav"))
	fmt.Println("too short: ", ulid.IsValid("01ARZ3NDEKTSV4RRFFQ69G5FA"))
	fmt.Println("bad letter:", ulid.IsValid("01ARZ3NDEKTSV4RRFFQ69G5FAI")) // I not in alphabet
	fmt.Println("empty:     ", ulid.IsValid(""))
	// Output:
	// canonical:  true
	// lowercase:  true
	// too short:  false
	// bad letter: false
	// empty:      false
}
