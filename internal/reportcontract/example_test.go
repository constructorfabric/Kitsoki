package reportcontract_test

import (
	"fmt"
	"strings"

	"kitsoki/internal/reportcontract"
)

func ExampleNormalizeDestination() {
	fmt.Println(reportcontract.NormalizeDestination(""))
	fmt.Println(reportcontract.NormalizeDestination("local-artifact"))
	fmt.Println(reportcontract.NormalizeDestination("ticket_provider"))
	// Output:
	// configured
	// local
	// ticket-provider
}

func ExampleBugFilerTools() {
	fmt.Println(strings.Join(reportcontract.BugFilerTools(), ", "))
	// Output:
	// Read, Glob, Grep, Bash(kitsoki bug create*)
}
