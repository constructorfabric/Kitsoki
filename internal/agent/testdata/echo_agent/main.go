// echo_agent is a minimal JSON-RPC 2.0 agent subprocess for testing.
//
// It reads newline-delimited JSON-RPC 2.0 requests on stdin and writes
// responses on stdout. The agent.ask method is supported; the response
// Submission is a JSON object echoing back key fields from the request:
//
//	{"echo_verb": "<verb>", "echo_prompt_head": "<first 50 chars of prompt>"}
//
// Special behaviours for testing failure modes:
//   - CRASH_BEFORE_RESPONSE=1: exit(1) before writing any response.
//   - CRASH_PARTIAL_RESPONSE=1: write a partial frame then exit(1).
//   - SLOW_RESPONSE_MS=<n>: sleep n milliseconds before responding.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type askRequest struct {
	Verb       string `json:"verb"`
	PromptText string `json:"prompt"`
}

type askResponse struct {
	Submission json.RawMessage `json:"submission,omitempty"`
	Meta       map[string]any  `json:"meta,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	crashBefore := os.Getenv("CRASH_BEFORE_RESPONSE") == "1"
	crashPartial := os.Getenv("CRASH_PARTIAL_RESPONSE") == "1"
	slowMS, _ := strconv.Atoi(os.Getenv("SLOW_RESPONSE_MS"))

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1*1024*1024), 1*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintf(os.Stderr, "echo_agent: unmarshal request: %v\n", err)
			os.Exit(1)
		}

		if crashBefore {
			os.Exit(1)
		}

		if crashPartial {
			// Write a partial frame (no newline, no closing brace).
			os.Stdout.Write([]byte(`{"jsonrpc":"2.0","id":`))
			os.Exit(1)
		}

		if slowMS > 0 {
			time.Sleep(time.Duration(slowMS) * time.Millisecond)
		}

		var resp rpcResponse
		resp.JSONRPC = "2.0"
		resp.ID = req.ID

		if req.Method != "agent.ask" {
			resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
		} else {
			var askReq askRequest
			_ = json.Unmarshal(req.Params, &askReq)

			promptHead := askReq.PromptText
			if len(promptHead) > 50 {
				promptHead = promptHead[:50]
			}
			sub, _ := json.Marshal(map[string]any{
				"echo_verb":        askReq.Verb,
				"echo_prompt_head": promptHead,
			})
			askResp := askResponse{
				Submission: json.RawMessage(sub),
				Meta:       map[string]any{"transport": "subprocess", "echo": true},
			}
			resultBytes, _ := json.Marshal(askResp)
			resp.Result = json.RawMessage(resultBytes)
		}

		out, _ := json.Marshal(resp)
		out = append(out, '\n')
		os.Stdout.Write(out)
	}
}
