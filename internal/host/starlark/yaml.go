package starlark

import (
	"fmt"

	goyaml "github.com/goccy/go-yaml"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// yamlModule is a deliberately tiny decode-only helper for story glue scripts.
// It lets Starlark inspect YAML manifests without granting filesystem writes,
// shell access, clocks, or any other nondeterministic surface.
var yamlModule = &starlarkstruct.Module{
	Name: "yaml",
	Members: starlark.StringDict{
		"decode": starlark.NewBuiltin("yaml.decode", yamlDecode),
	},
}

func yamlDecode(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var src string
	if err := starlark.UnpackArgs("yaml.decode", args, kwargs, "src", &src); err != nil {
		return nil, err
	}
	var v any
	if err := goyaml.Unmarshal([]byte(src), &v); err != nil {
		return nil, fmt.Errorf("yaml.decode: %w", err)
	}
	return goToStarlark(normalizeYAML(v))
}

func normalizeYAML(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = normalizeYAML(v)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[fmt.Sprint(k)] = normalizeYAML(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = normalizeYAML(v)
		}
		return out
	case uint:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case uint64:
		if x <= uint64(^uint64(0)>>1) {
			return int64(x)
		}
		return float64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	default:
		return v
	}
}
