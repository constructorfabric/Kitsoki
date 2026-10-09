package host

import (
	"fmt"
	"strings"
)

func agentRunErrorMessage(verb string, err error, stderr string) string {
	msg := fmt.Sprintf("host.agent.%s: agent stream failed: %v", verb, err)
	if s := strings.TrimSpace(stderr); s != "" {
		msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
	}
	return msg
}
