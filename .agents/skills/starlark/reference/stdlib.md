# Kitsoki Starlark Standard Library

Kitsoki exposes a deliberately tiny deterministic stdlib to both
`host.starlark.run` scripts and `host.agent.codeact` snippets. No module is
loaded from the filesystem. No module exposes clocks, randomness, environment,
process execution, or network access. External data must enter through
`ctx.inputs`, `ctx.world.get`, or an explicitly granted `ctx` capability.

By default the predeclared modules are:

| Module | Source | Surface |
|---|---|---|
| `json` | `go.starlark.net/lib/json` | JSON encode/decode/pretty-print helpers. |
| `math` | `go.starlark.net/lib/math` | Deterministic numeric functions and constants. |
| `yaml` | Kitsoki helper in `internal/host/starlark/yaml.go` | Decode-only YAML parser. |

An effect can narrow the module set with `with.capabilities.stdlib`, but it
cannot add modules outside `json`, `math`, and `yaml`.

```yaml
capabilities:
  stdlib: [json, yaml]  # math is intentionally absent for this run
```

## `json`

`json` is `go.starlark.net/lib/json`.

| Function | Signature | Notes |
|---|---|---|
| `json.decode` | `json.decode(x, default=None)` | Parses a JSON string into Starlark values. Invalid JSON returns `default` when provided, otherwise fails. Integers become Starlark `int`; decimal/exponent numbers become `float`; objects become dicts; arrays become lists. |
| `json.encode` | `json.encode(x)` | Encodes a Starlark value to compact JSON. Dict/object keys must be strings and are emitted in sorted order. Non-finite floats fail. |
| `json.encode_indent` | `json.encode_indent(x, *, prefix="", indent="\t")` | Equivalent to encoding then pretty-printing. |
| `json.indent` | `json.indent(str, *, prefix="", indent="\t")` | Pretty-prints an existing valid JSON string. |

```python
def main(ctx):
    payload = json.decode(ctx.inputs["payload_json"], default={})
    payload["source"] = "cassette"
    return {"payload_json": json.encode(payload)}
```

## `math`

`math` is `go.starlark.net/lib/math`. All functions accept Starlark `int` and
`float` arguments unless noted.

| Category | Names |
|---|---|
| Rounding and signs | `ceil`, `floor`, `round`, `fabs`, `copysign` |
| Powers and roots | `pow`, `sqrt`, `exp` |
| Remainders | `mod`, `remainder` |
| Logarithms | `log(x, base=math.e)` |
| Trigonometry | `acos`, `asin`, `atan`, `atan2`, `cos`, `hypot`, `sin`, `tan` |
| Angle conversion | `degrees`, `radians` |
| Hyperbolic | `acosh`, `asinh`, `atanh`, `cosh`, `sinh`, `tanh` |
| Special | `gamma` |
| Constants | `e`, `pi` |

`ceil` and `floor` return an integer. Most other numeric functions return a
float.

```python
def main(ctx):
    raw = float(ctx.inputs["score"])
    pct = int(math.round(raw * 100))
    return {"percent": pct}
```

Starlark's `%` string formatting does not support precision such as `%.2f`.
For fixed-width decimal strings, round with `math` and assemble the string
explicitly.

## `yaml`

`yaml` is a Kitsoki-owned decode-only module.

| Function | Signature | Notes |
|---|---|---|
| `yaml.decode` | `yaml.decode(src)` | Parses a YAML string into Starlark dict/list/scalar values. Mapping keys are stringified. Integer-like YAML values are normalized to Starlark integers where possible. |

There is no `yaml.encode`. Return structured values from `main(ctx)` and let
Kitsoki bind them, or use `json.encode` when a string representation is needed.

```python
def main(ctx):
    manifest = yaml.decode(ctx.inputs["app_yaml"])
    app = manifest.get("app", {})
    return {"app_id": app.get("id", "")}
```

## What Is Deliberately Missing

- `time`, `random`, `os`, `path`, `re`, `struct`, `proto`, `load`, and imports.
- Filesystem or network helpers outside explicit `ctx.fs` and `ctx.http`
  grants.
- Shell/process execution. Use `ctx.probe` for fixed read-only probes, or
  `host.run` / `host.agent.task` when the story truly needs commands.
- Mutable world. Return outputs and bind them through the effect.

If a script seems to need a missing module, first check whether the value should
be an input, a world key, a small pure helper in the script, or a separately
granted `ctx` capability. Add a Go host handler only when the behavior is shared
infrastructure rather than story glue.
