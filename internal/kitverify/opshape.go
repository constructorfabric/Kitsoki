package kitverify

import (
	"fmt"
	"sort"

	"kitsoki/internal/app"
	"kitsoki/internal/host/opschema"
	starlarkhost "kitsoki/internal/host/starlark"
)

// CheckInterfaceOpShapes verifies that each host_interface's declared
// operations.<op>.input/output field types are honored by whatever
// concretely backs iface.Default:
//
//   - a starlark-script binding: when def.StarlarkHostBindings[iface.Default]
//     is set (the S3a synthetic-handler-name convention — see
//     internal/app/imports.go's resolveHostBindingScripts), the script's
//     .star.yaml sidecar is authoritative, so this is checked against
//     Sidecar.Inputs/Outputs — "nearly free," per the plan doc, since the
//     sidecar already exists for an unrelated reason (the engine validates
//     the script's own I/O against it at every host.starlark.run call).
//   - a plain Go handler name: checked against registry's registered Op,
//     when one exists. An UNREGISTERED handler produces no error — most of
//     the engine's host.* surface has no entry in the table yet (see
//     opschema.Builtins' doc comment) — "no registered schema" reads as
//     "cannot check", not "fails", so a kit that binds to some other
//     builtin handler isn't penalized for a gap in the seed table.
//
// registry may be nil, equivalent to opschema.NewRegistry() (only
// starlark-bound interfaces get checked).
func CheckInterfaceOpShapes(def *app.AppDef, registry *opschema.Registry) []string {
	if def == nil {
		return nil
	}
	var out []string
	for _, name := range sortedIfaceNames(def.HostInterfaces) {
		iface := def.HostInterfaces[name]
		if iface == nil || iface.Default == "" {
			continue // resolveAllInterfaces already rejects this at fold time.
		}
		if scriptPath, ok := def.StarlarkHostBindings[iface.Default]; ok {
			out = append(out, checkAgainstSidecar(name, iface, scriptPath)...)
			continue
		}
		out = append(out, checkAgainstRegistry(name, iface, registry)...)
	}
	return out
}

func sortedIfaceNames(m map[string]*app.HostInterfaceDef) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedOpNames(m map[string]*app.HostInterfaceOp) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedFieldNames(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func checkAgainstSidecar(ifaceName string, iface *app.HostInterfaceDef, scriptPath string) []string {
	sidecar, err := starlarkhost.LoadSidecar(scriptPath + ".yaml")
	if err != nil {
		return []string{fmt.Sprintf(
			"host_interfaces.%s: default %q is starlark-bound but its sidecar failed to load: %v",
			ifaceName, iface.Default, err)}
	}
	var out []string
	for _, op := range sortedOpNames(iface.Operations) {
		opDef := iface.Operations[op]
		for _, field := range sortedFieldNames(opDef.Input) {
			t := bareType(opDef.Input[field])
			spec, ok := sidecar.Inputs[field]
			if !ok {
				out = append(out, fmt.Sprintf(
					"host_interfaces.%s.%s: input %q has no matching entry in the starlark sidecar's inputs:", ifaceName, op, field))
				continue
			}
			if !typesCompatible(t, spec.Type) {
				out = append(out, fmt.Sprintf(
					"host_interfaces.%s.%s: input %q declared type %q but the sidecar declares %q", ifaceName, op, field, t, spec.Type))
			}
		}
		for _, field := range sortedFieldNames(opDef.Output) {
			t := bareType(opDef.Output[field])
			spec, ok := sidecar.Outputs[field]
			if !ok {
				out = append(out, fmt.Sprintf(
					"host_interfaces.%s.%s: output %q has no matching entry in the starlark sidecar's outputs: (the script will never emit it)", ifaceName, op, field))
				continue
			}
			if !typesCompatible(t, spec.Type) {
				out = append(out, fmt.Sprintf(
					"host_interfaces.%s.%s: output %q declared type %q but the sidecar declares %q", ifaceName, op, field, t, spec.Type))
			}
		}
	}
	return out
}

func checkAgainstRegistry(ifaceName string, iface *app.HostInterfaceDef, registry *opschema.Registry) []string {
	if registry == nil {
		return nil
	}
	var out []string
	for _, op := range sortedOpNames(iface.Operations) {
		opDef := iface.Operations[op]
		spec, ok := registry.Lookup(iface.Default, op)
		if !ok {
			continue // unregistered handler — cannot check, not a failure.
		}
		for _, field := range sortedFieldNames(opDef.Input) {
			t := bareType(opDef.Input[field])
			want, ok := spec.Input[field]
			if !ok {
				out = append(out, fmt.Sprintf(
					"host_interfaces.%s.%s: input %q has no matching entry in %s's registered schema", ifaceName, op, field, iface.Default))
				continue
			}
			if !typesCompatible(t, want.Type) {
				out = append(out, fmt.Sprintf(
					"host_interfaces.%s.%s: input %q declared type %q but %s's registered schema declares %q", ifaceName, op, field, t, iface.Default, want.Type))
			}
		}
		for _, field := range sortedFieldNames(opDef.Output) {
			t := bareType(opDef.Output[field])
			want, ok := spec.Output[field]
			if !ok {
				out = append(out, fmt.Sprintf(
					"host_interfaces.%s.%s: output %q has no matching entry in %s's registered schema", ifaceName, op, field, iface.Default))
				continue
			}
			if !typesCompatible(t, want.Type) {
				out = append(out, fmt.Sprintf(
					"host_interfaces.%s.%s: output %q declared type %q but %s's registered schema declares %q", ifaceName, op, field, t, iface.Default, want.Type))
			}
		}
	}
	return out
}

// bareType extracts a host_interfaces operations.input/output field's type
// string. Fields are authored as bare scalars (`query: string`); anything
// else (a nested map, list, ...) is treated as "object" for comparison
// purposes since op shape declarations don't support nested schemas yet.
func bareType(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return "object"
}

// typesCompatible compares a host_interfaces bare type string against a
// starlark FieldSpec / opschema.FieldSpec type string. "any" on either side
// matches everything; "int"/"number"/"float" are treated as one interchangeable
// numeric family (mirroring internal/host/starlark's own int/number blur
// across the JSON/Starlark boundary — see starlark.FieldSpec's doc comment).
func typesCompatible(a, b string) bool {
	if a == "" || b == "" || a == "any" || b == "any" {
		return true
	}
	return normType(a) == normType(b)
}

func normType(t string) string {
	switch t {
	case "int", "number", "float":
		return "number"
	default:
		return t
	}
}
