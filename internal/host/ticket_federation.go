package host

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

const ticketFederationHost = "host.ticket_federation"

type ticketFederationSource struct {
	ID       string
	Label    string
	Provider string
	Kind     string
	Mode     string
	Args     map[string]any
	// LocatorKeys names source-owned args that must beat ambient runtime
	// values. repo/root/workdir are implicit for compatibility; future
	// providers declare keys such as project or queue explicitly.
	LocatorKeys map[string]struct{}
}

type ticketFederationSourceResult struct {
	Source   ticketFederationSource
	Tickets  []map[string]any
	Repo     string
	Error    string
	Warnings []string
}

// TicketFederationHandler composes any number of explicitly registered ticket
// providers behind one ticket host_interface binding. Search/list operations
// fan out and retain a deterministic source order. Record and mutation
// operations route to exactly one source, so provider-local ids (notably the
// same GitHub issue number in two repositories) can never cross wires.
//
// Provider names come from configuration data, but nested dispatch is allowed
// only for handlers registered through Registry.RegisterTicketProvider. This is
// the capability boundary that prevents a warp or stale profile from smuggling
// an arbitrary host.run/host.git call through the federation.
func TicketFederationHandler(reg *Registry) Handler {
	return func(ctx context.Context, args map[string]any) (Result, error) {
		if reg == nil {
			return Result{Error: "host.ticket_federation: host registry is required"}, nil
		}
		op := strings.TrimSpace(ghStr(args["op"]))
		if op == "" {
			return Result{Error: "host.ticket_federation: op argument is required"}, nil
		}
		sources, err := parseTicketFederationSources(args["sources"])
		if err != nil {
			return Result{Error: "host.ticket_federation: " + err.Error()}, nil
		}
		for _, source := range sources {
			if source.Provider == ticketFederationHost {
				return Result{Error: fmt.Sprintf("host.ticket_federation: source %q cannot recursively use %s", source.ID, ticketFederationHost)}, nil
			}
			if !reg.IsTicketProvider(source.Provider) {
				return Result{Error: fmt.Sprintf("host.ticket_federation: source %q provider %q is not a registered ticket provider", source.ID, source.Provider)}, nil
			}
		}

		switch op {
		case "search", "list_mine":
			return ticketFederationList(ctx, reg, sources, args, op), nil
		case "get", "create", "comment", "comment_edit", "comment_reactions", "transition", "assign", "unassign":
			return ticketFederationRoute(ctx, reg, sources, args, op)
		default:
			return Result{Error: fmt.Sprintf("host.ticket_federation: unknown op %q", op)}, nil
		}
	}
}

func parseTicketFederationSources(raw any) ([]ticketFederationSource, error) {
	var rows []map[string]any
	switch v := raw.(type) {
	case []map[string]any:
		rows = v
	case []any:
		rows = make([]map[string]any, 0, len(v))
		for i, item := range v {
			row, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("sources[%d] must be an object", i)
			}
			rows = append(rows, row)
		}
	case nil:
		return nil, fmt.Errorf("sources argument is required")
	default:
		return nil, fmt.Errorf("sources must be a list")
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("at least one ticket source is required")
	}

	seen := make(map[string]struct{}, len(rows))
	seenLabels := make(map[string]struct{}, len(rows))
	out := make([]ticketFederationSource, 0, len(rows))
	for i, row := range rows {
		source := ticketFederationSource{
			ID:       strings.TrimSpace(ghStr(row["id"])),
			Label:    strings.TrimSpace(ghStr(row["label"])),
			Provider: strings.TrimSpace(ghStr(row["provider"])),
			Kind:     strings.ToLower(strings.TrimSpace(ghStr(row["kind"]))),
			Mode:     strings.ToLower(strings.TrimSpace(ghStr(row["mode"]))),
		}
		if source.ID == "" {
			return nil, fmt.Errorf("sources[%d].id is required", i)
		}
		if !validTicketSourceID(source.ID) {
			return nil, fmt.Errorf("sources[%d].id %q may contain only letters, digits, '.', '_' and '-'", i, source.ID)
		}
		if _, exists := seen[source.ID]; exists {
			return nil, fmt.Errorf("duplicate source id %q", source.ID)
		}
		seen[source.ID] = struct{}{}
		if source.Provider == "" {
			return nil, fmt.Errorf("source %q provider is required", source.ID)
		}
		if source.Label != "" {
			labelKey := strings.ToLower(source.Label)
			if _, exists := seenLabels[labelKey]; exists {
				return nil, fmt.Errorf("duplicate source label %q", source.Label)
			}
			seenLabels[labelKey] = struct{}{}
		}
		if source.Kind == "" {
			source.Kind = "ticket"
		}
		if source.Mode == "" {
			if source.Kind == "local" {
				source.Mode = "local"
			} else {
				source.Mode = "remote"
			}
		}
		if source.Mode != "local" && source.Mode != "remote" {
			return nil, fmt.Errorf("source %q mode must be local or remote", source.ID)
		}
		if rawArgs := row["args"]; rawArgs != nil {
			m, ok := rawArgs.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("source %q args must be an object", source.ID)
			}
			source.Args = copyStringMap(m)
		}
		source.LocatorKeys = map[string]struct{}{}
		for _, key := range []string{"repo", "root", "workdir"} {
			if _, ok := source.Args[key]; ok {
				source.LocatorKeys[key] = struct{}{}
			}
		}
		locatorKeys, keyErr := ticketFederationStringList(row["locator_keys"])
		if keyErr != nil {
			return nil, fmt.Errorf("source %q locator_keys: %w", source.ID, keyErr)
		}
		for _, key := range locatorKeys {
			if ticketFederationDynamicArg(key) || ticketFederationControlArg(key) {
				return nil, fmt.Errorf("source %q locator key %q is reserved for live operation data", source.ID, key)
			}
			if _, ok := source.Args[key]; !ok {
				return nil, fmt.Errorf("source %q locator key %q is not present in args", source.ID, key)
			}
			source.LocatorKeys[key] = struct{}{}
		}
		out = append(out, source)
	}
	return out, nil
}

func ticketFederationStringList(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	var values []any
	switch typed := raw.(type) {
	case []any:
		values = typed
	case []string:
		values = make([]any, len(typed))
		for i, value := range typed {
			values[i] = value
		}
	default:
		return nil, fmt.Errorf("must be a list of strings")
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		key, ok := value.(string)
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("must contain only non-empty strings")
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("contains duplicate key %q", key)
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out, nil
}

func validTicketSourceID(id string) bool {
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return id != ""
}

func ticketFederationList(ctx context.Context, reg *Registry, sources []ticketFederationSource, runtimeArgs map[string]any, op string) Result {
	results := make([]ticketFederationSourceResult, len(sources))
	var wg sync.WaitGroup
	for i := range sources {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = invokeTicketFederationSource(ctx, reg, sources[i], runtimeArgs, op, "")
		}()
	}
	wg.Wait()

	// A migrated remote ticket is the durable identity. Remove only the local
	// row carrying its legacy id; never collapse two remote rows merely because
	// two repositories happen to use the same numeric issue id.
	localSourceIDs := []string{}
	for _, result := range results {
		if result.Source.Mode == "local" {
			localSourceIDs = append(localSourceIDs, result.Source.ID)
		}
	}
	migratedLocalIDs := map[string]map[string]struct{}{}
	markMigrated := func(sourceID, id string) {
		if sourceID == "" || id == "" {
			return
		}
		if migratedLocalIDs[sourceID] == nil {
			migratedLocalIDs[sourceID] = map[string]struct{}{}
		}
		migratedLocalIDs[sourceID][id] = struct{}{}
	}
	for _, result := range results {
		if result.Source.Mode == "local" {
			continue
		}
		for _, row := range result.Tickets {
			legacyID := strings.TrimSpace(ghStr(row["legacy_id"]))
			legacySource := strings.TrimSpace(ghStr(row["legacy_source"]))
			if ref := strings.TrimSpace(ghStr(row["legacy_ref"])); ref != "" {
				if source, id, ok := ticketFederationSourceFromRef(sources, ref); ok && source.Mode == "local" {
					markMigrated(source.ID, id)
					continue
				}
			}
			if legacySource != "" && legacyID != "" {
				if source, ok := ticketFederationSourceByID(sources, legacySource); ok && source.Mode == "local" {
					markMigrated(source.ID, legacyID)
				}
				continue
			}
			// The historical metadata block carried only legacy_id. It remains
			// safe when composition has exactly one local store; with multiple
			// stores, suppress nothing unless legacy_source/ref disambiguates it.
			if legacyID != "" && len(localSourceIDs) == 1 {
				markMigrated(localSourceIDs[0], legacyID)
			}
		}
	}
	if len(migratedLocalIDs) > 0 {
		for i := range results {
			if results[i].Source.Mode != "local" {
				continue
			}
			kept := results[i].Tickets[:0]
			for _, row := range results[i].Tickets {
				if _, migrated := migratedLocalIDs[results[i].Source.ID][strings.TrimSpace(ghStr(row["id"]))]; !migrated {
					kept = append(kept, row)
				}
			}
			results[i].Tickets = kept
		}
	}

	flat := make([]map[string]any, 0)
	groups := make([]map[string]any, 0, len(results))
	providerErrors := make([]string, 0)
	counts := make(map[string]any, len(results))
	localCount, githubCount := 0, 0
	for i := range results {
		result := &results[i]
		for _, row := range result.Tickets {
			row["index"] = len(flat) + 1
			flat = append(flat, row)
		}
		label := ticketFederationSourceLabel(result.Source, result.Repo)
		group := map[string]any{
			"id":      result.Source.ID,
			"label":   label,
			"kind":    result.Source.Kind,
			"mode":    result.Source.Mode,
			"repo":    result.Repo,
			"count":   len(result.Tickets),
			"tickets": result.Tickets,
		}
		if result.Error != "" {
			group["error"] = result.Error
			providerErrors = append(providerErrors, label+": "+result.Error)
		}
		if len(result.Warnings) > 0 {
			group["warnings"] = append([]string(nil), result.Warnings...)
			for _, warning := range result.Warnings {
				providerErrors = append(providerErrors, label+": "+warning)
			}
		}
		groups = append(groups, group)
		counts[result.Source.ID] = len(result.Tickets)
		if result.Source.Mode == "local" {
			localCount += len(result.Tickets)
		}
		if result.Source.Kind == "github" {
			githubCount += len(result.Tickets)
		}
	}

	return Result{Data: map[string]any{
		"tickets":              flat,
		"source_groups":        groups,
		"source_counts":        counts,
		"provider_errors":      providerErrors,
		"local_count":          localCount,
		"github_count":         githubCount,
		"ticket_local_count":   localCount,
		"ticket_github_count":  githubCount,
		"ticket_source_counts": counts,
	}}
}

func invokeTicketFederationSource(ctx context.Context, reg *Registry, source ticketFederationSource, runtimeArgs map[string]any, op, forcedID string) ticketFederationSourceResult {
	callArgs := ticketFederationProviderArgs(runtimeArgs, source.Args, source.LocatorKeys, op)
	if forcedID != "" {
		callArgs["id"] = forcedID
	}
	res, err := reg.Invoke(ctx, source.Provider+"."+op, callArgs)
	result := ticketFederationSourceResult{Source: source}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if res.Error != "" {
		result.Error = res.Error
	}
	result.Repo = ticketFederationResultRepo(source, callArgs, res.Data)
	result.Warnings = ticketFederationWarnings(res.Data["provider_errors"])
	result.Tickets = ticketRows(res.Data["tickets"])
	for _, row := range result.Tickets {
		annotateFederatedTicket(row, source, result.Repo)
	}
	return result
}

func ticketFederationProviderArgs(runtimeArgs, sourceArgs map[string]any, locatorKeys map[string]struct{}, op string) map[string]any {
	out := make(map[string]any, len(runtimeArgs)+len(sourceArgs)+1)
	// Source args provide provider-specific defaults. Known live payload fields
	// are never accepted from configuration, and any runtime field wins below;
	// this keeps future-provider payloads safe without maintaining an exhaustive
	// list of every provider's mutation vocabulary.
	for k, v := range sourceArgs {
		if ticketFederationDynamicArg(k) || ticketFederationControlArg(k) {
			continue
		}
		out[k] = v
	}
	for k, v := range runtimeArgs {
		if ticketFederationControlArg(k) {
			continue
		}
		out[k] = v
	}
	// dev-story still sends legacy ambient repo/root/workdir arguments on every
	// interface call. A source's explicit locator must beat those ambient
	// fallbacks; other, provider-specific runtime values remain live-call data.
	for k := range locatorKeys {
		if v, ok := sourceArgs[k]; ok {
			out[k] = v
		}
	}
	out["op"] = op
	return out
}

func ticketFederationControlArg(key string) bool {
	switch key {
	case "sources", "source", "source_id", "source_ref", "ref":
		return true
	default:
		return false
	}
}

func ticketFederationDynamicArg(key string) bool {
	switch key {
	case "op", "id", "body", "thread", "to", "query", "limit", "filter", "comment_id", "assignee",
		"title", "description", "severity", "component", "target", "trace_ref", "filed_by",
		"labels", "state":
		return true
	default:
		return false
	}
}

func ticketFederationRoute(ctx context.Context, reg *Registry, sources []ticketFederationSource, args map[string]any, op string) (Result, error) {
	source, localID, err := selectTicketFederationSource(ctx, sources, args)
	if err != nil {
		return Result{Error: "host.ticket_federation: " + err.Error()}, nil
	}
	callArgs := ticketFederationProviderArgs(args, source.Args, source.LocatorKeys, op)
	if localID != "" {
		callArgs["id"] = localID
	}
	res, invokeErr := reg.Invoke(ctx, source.Provider+"."+op, callArgs)
	if invokeErr != nil {
		return Result{}, invokeErr
	}
	data := copyStringMap(res.Data)
	if data == nil {
		data = map[string]any{}
	}
	repo := ticketFederationResultRepo(source, callArgs, data)
	annotateFederatedTicket(data, source, repo)
	if strings.TrimSpace(ghStr(data["id"])) == "" && localID != "" {
		data["id"] = localID
		data["ref"] = source.ID + ":" + localID
	}
	if res.Error != "" {
		return Result{Data: data, Error: ticketFederationSourceLabel(source, repo) + ": " + res.Error}, nil
	}
	return Result{Data: data}, nil
}

func selectTicketFederationSource(ctx context.Context, sources []ticketFederationSource, args map[string]any) (ticketFederationSource, string, error) {
	id := strings.TrimSpace(ghStr(args["id"]))
	selector := strings.TrimSpace(ghStr(args["source_id"]))
	if selector == "" {
		selector = strings.TrimSpace(ghStr(args["source"]))
	}
	if ref := strings.TrimSpace(ghStr(args["ref"])); ref != "" {
		if source, localID, ok := ticketFederationSourceFromRef(sources, ref); ok {
			return source, localID, nil
		}
		// A raw GitHub URL or owner/repo#N value is a ticket identifier, not
		// necessarily a federation-qualified ref. Let repository inference below
		// select its source. Only fail early when the value actually has the
		// source-id:id shape but names a source that is not configured.
		if looksLikeTicketFederationRef(ref) {
			return ticketFederationSource{}, "", fmt.Errorf("ticket ref %q names an unknown source", ref)
		}
		id = ref
	}
	if source, localID, ok := ticketFederationSourceFromRef(sources, id); ok {
		return source, localID, nil
	}
	if selector != "" {
		if source, ok := uniqueTicketFederationSource(sources, selector); ok {
			return source, id, nil
		}
		return ticketFederationSource{}, "", fmt.Errorf("ticket source %q is unknown or ambiguous", selector)
	}
	// Identity encoded in a raw URL or owner/repo#N ref is authoritative. It
	// must beat any stale ambient repo left from a previously selected ticket.
	if repo := ticketFederationRepoFromRef(id); repo != "" {
		matches := ticketFederationSourcesByRepo(ctx, sources, repo, args)
		if len(matches) == 1 {
			return matches[0], id, nil
		}
		if len(matches) > 1 {
			return ticketFederationSource{}, "", fmt.Errorf("ticket repository %q matches multiple sources", repo)
		}
	}
	// Legacy/autonomous callers commonly carry a provider-local id plus an
	// explicit repository instead of source/ref metadata. Match that repository
	// to exactly one configured source. A source id (for example "origin") wins
	// before owner/repo resolution so managed clones do not need matching remote
	// aliases.
	if repoSelector := strings.TrimSpace(ghStr(args["repo"])); repoSelector != "" {
		if source, ok := ticketFederationSourceByID(sources, repoSelector); ok {
			return source, id, nil
		}
		resolvedSelector := resolveTicketRepo(ctx, repoSelector, args)
		matches := ticketFederationSourcesByRepo(ctx, sources, resolvedSelector, args)
		if len(matches) == 1 {
			return matches[0], id, nil
		}
		if len(matches) > 1 {
			return ticketFederationSource{}, "", fmt.Errorf("ticket repository %q matches multiple sources", resolvedSelector)
		}
	}
	if len(sources) == 1 {
		return sources[0], id, nil
	}
	return ticketFederationSource{}, "", fmt.Errorf("source_id or source-qualified ref is required with %d ticket sources", len(sources))
}

func looksLikeTicketFederationRef(ref string) bool {
	if strings.Contains(ref, "://") {
		return false
	}
	prefix, _, ok := strings.Cut(ref, ":")
	return ok && validTicketSourceID(prefix)
}

func ticketFederationSourceFromRef(sources []ticketFederationSource, ref string) (ticketFederationSource, string, bool) {
	for _, source := range sources {
		prefix := source.ID + ":"
		if strings.HasPrefix(ref, prefix) {
			return source, strings.TrimPrefix(ref, prefix), true
		}
	}
	return ticketFederationSource{}, "", false
}

func uniqueTicketFederationSource(sources []ticketFederationSource, selector string) (ticketFederationSource, bool) {
	// Exact configured identity always wins. Kind is only a convenience alias
	// when precisely one source of that kind exists.
	if source, ok := ticketFederationSourceByID(sources, selector); ok {
		return source, true
	}
	var matches []ticketFederationSource
	for _, source := range sources {
		if source.Kind == selector {
			matches = append(matches, source)
		}
	}
	if len(matches) != 1 {
		return ticketFederationSource{}, false
	}
	return matches[0], true
}

func ticketFederationSourceByID(sources []ticketFederationSource, id string) (ticketFederationSource, bool) {
	for _, source := range sources {
		if source.ID == id {
			return source, true
		}
	}
	return ticketFederationSource{}, false
}

func ticketFederationSourcesByRepo(ctx context.Context, sources []ticketFederationSource, repo string, args map[string]any) []ticketFederationSource {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil
	}
	matches := make([]ticketFederationSource, 0, 1)
	for _, source := range sources {
		configured := strings.TrimSpace(ghStr(source.Args["repo"]))
		if configured == "" {
			continue
		}
		resolved := configured
		if source.Provider == "host.gh.ticket" {
			resolved = resolveTicketRepo(ctx, configured, ticketFederationProviderArgs(args, source.Args, source.LocatorKeys, "get"))
		}
		if strings.EqualFold(strings.TrimSpace(resolved), repo) {
			matches = append(matches, source)
		}
	}
	return matches
}

func ticketFederationRepoFromRef(ref string) string {
	repo, _ := splitIssueID(ref)
	return strings.TrimSpace(repo)
}

func ticketFederationResultRepo(source ticketFederationSource, callArgs, data map[string]any) string {
	for _, key := range []string{"source_repo", "ticket_repo", "repo"} {
		if repo := strings.TrimSpace(ghStr(data[key])); repo != "" {
			return repo
		}
	}
	if source.Mode == "local" {
		// Federation calls may retain legacy ambient repo args alongside the
		// configured sources list. That value belongs to remote compatibility
		// calls and must not make a local-file source look repository-backed.
		return strings.TrimSpace(ghStr(source.Args["repo"]))
	}
	return strings.TrimSpace(ghStr(callArgs["repo"]))
}

func ticketFederationSourceLabel(source ticketFederationSource, repo string) string {
	if source.Label != "" {
		return source.Label
	}
	if strings.TrimSpace(repo) != "" {
		return strings.TrimSpace(repo)
	}
	return source.ID
}

func annotateFederatedTicket(row map[string]any, source ticketFederationSource, repo string) {
	if row == nil {
		return
	}
	label := ticketFederationSourceLabel(source, repo)
	row["source"] = source.ID
	row["source_id"] = source.ID
	row["source_label"] = label
	row["source_kind"] = source.Kind
	row["source_mode"] = source.Mode
	row["source_repo"] = repo
	if source.Mode == "remote" && repo != "" {
		row["ticket_repo"] = repo
	}
	if id := strings.TrimSpace(ghStr(row["id"])); id != "" {
		row["ref"] = source.ID + ":" + id
	}
}

func ticketFederationWarnings(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if warning := strings.TrimSpace(fmt.Sprint(item)); warning != "" {
				out = append(out, warning)
			}
		}
		return out
	case string:
		if strings.TrimSpace(v) != "" {
			return []string{strings.TrimSpace(v)}
		}
	}
	return nil
}
