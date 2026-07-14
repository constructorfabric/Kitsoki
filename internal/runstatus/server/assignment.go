package server

import (
	"encoding/json"
	"errors"
	"strings"

	"kitsoki/internal/assignment"
)

// dispatchAssignment is deliberately separate from prompt/turn processing:
// an assignment is a policy-checked runtime event, never authority inferred
// from an agent prompt or a story-YAML mutation.
func (s *Server) dispatchAssignment(method string, params map[string]any) (any, *rpcError) {
	if s.assignments == nil {
		return nil, readOnlyErr(method)
	}
	entry, rerr := s.resolve(params)
	if rerr != nil {
		return nil, rerr
	}
	sessionID, _ := params["session_id"].(string)
	if method == "runstatus.assignment.list" {
		out, err := s.assignments.List(sessionID)
		if err != nil {
			return nil, serverErr(err)
		}
		return out, nil
	}
	room, _ := params["room_path"].(string)
	room = strings.TrimSpace(room)
	state, ok := entry.Source.AppDef().States[room]
	if room == "" || !ok || state.Assignment == nil {
		return nil, &rpcError{Code: codeNotFound, Message: "assignment: unknown or unassignable room: " + room}
	}
	if method == "runstatus.assignment.get" {
		record, found, err := s.assignments.Get(sessionID, room)
		if err != nil {
			return nil, serverErr(err)
		}
		return map[string]any{"assignment": record, "found": found, "policy": state.Assignment}, nil
	}
	principal, _ := params["principal"].(string)
	if method != "runstatus.assignment.unassign" && strings.TrimSpace(principal) == "" {
		return nil, &rpcError{Code: codeServerError, Message: "assignment: missing 'principal'"}
	}
	expected, ok := intParam(params, "expected_version")
	if !ok {
		return nil, &rpcError{Code: codeServerError, Message: "assignment: missing 'expected_version'"}
	}
	current, found, err := s.assignments.Get(sessionID, room)
	if err != nil {
		return nil, serverErr(err)
	}
	if method == "runstatus.assignment.reassign" && (!found || !state.Assignment.AllowReassign) {
		return nil, &rpcError{Code: codeServerError, Message: "assignment: reassign is not permitted by room policy"}
	}
	if method == "runstatus.assignment.assign" && found && current.Principal != "" {
		return nil, &rpcError{Code: codeServerError, Message: "assignment: room is already assigned; use reassign"}
	}
	next, err := s.assignments.Change(assignment.Record{SessionID: sessionID, RoomPath: room, Principal: principal, Source: "kitsoki"}, int64(expected))
	if err != nil {
		var stale *assignment.StaleVersionError
		if errors.As(err, &stale) {
			data, _ := json.Marshal(stale.Current)
			return nil, &rpcError{Code: codeStaleVersion, Message: "assignment: stale version", Data: string(data)}
		}
		return nil, serverErr(err)
	}
	return next, nil
}
