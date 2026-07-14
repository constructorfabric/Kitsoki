package tourspec

import "fmt"

// SubjectIDs are the only client-controlled identifiers accepted at review
// entry. Room, tool, capability and policy selection stay in trusted metadata.
type SubjectIDs struct {
	ProjectID  string `json:"project_id"`
	SessionID  string `json:"session_id"`
	ArtifactID string `json:"artifact_id"`
}

// EntryPoint is trusted, server-owned review metadata.
type EntryPoint struct {
	BindingID        string         `json:"binding_id"`
	CanonicalRoom    string         `json:"canonical_room"`
	SessionKey       string         `json:"session_key"`
	RevisionPolicy   string         `json:"revision_policy"`
	AssignmentPolicy string         `json:"assignment_policy"`
	Prerequisites    []string       `json:"prerequisites"`
	RequestSchema    map[string]any `json:"request_schema"`
	FormSchema       map[string]any `json:"form_schema"`
	Defaults         map[string]any `json:"defaults"`
}

// Registry is injected by the server from story metadata. It intentionally has
// no method that accepts a client-provided room or capability name.
type Registry map[string]EntryPoint

// Resolve returns a complete server-resolved entrypoint for typed subject ids.
func (r Registry) Resolve(bindingID string, subject SubjectIDs) (EntryPoint, error) {
	if bindingID == "" || !idRE.MatchString(bindingID) {
		return EntryPoint{}, fmt.Errorf("trusted binding id must be kebab-case")
	}
	if subject.ProjectID == "" || subject.SessionID == "" || subject.ArtifactID == "" {
		return EntryPoint{}, fmt.Errorf("project_id, session_id, and artifact_id are required")
	}
	e, ok := r[bindingID]
	if !ok {
		return EntryPoint{}, fmt.Errorf("unknown trusted binding %q", bindingID)
	}
	if e.BindingID != bindingID || e.CanonicalRoom == "" || e.SessionKey == "" || e.RevisionPolicy == "" || e.AssignmentPolicy == "" {
		return EntryPoint{}, fmt.Errorf("trusted binding %q has incomplete metadata", bindingID)
	}
	return e, nil
}
