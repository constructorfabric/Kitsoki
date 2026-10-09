// Package kitver implements the tiny semver-constraint matcher that consumes
// ImportDef.Version (internal/app/types.go). Before S2, Version was parsed
// and stored but never checked against anything (see the doc comment on
// ImportDef.Version) — this package is the missing piece: given the
// resolved child manifest's own `app.version:` and the importer's
// `imports.<alias>.version:` constraint, Satisfies reports whether the
// resolved candidate is acceptable.
//
// # Constraint syntax
//
// A constraint is one of:
//
//	""            any version satisfies (no constraint declared)
//	"*"           any version satisfies (explicit form of the above)
//	"1.2.3"       exact match only
//	"=1.2.3"      exact match only (explicit form)
//	">=1.2.3"     greater-than-or-equal
//	">1.2.3"      strictly greater
//	"<=1.2.3"     less-than-or-equal
//	"<1.2.3"      strictly less
//	"^1.2.3"      caret range: >=1.2.3, <2.0.0 (>=0.2.3,<0.3.0 when major==0,
//	              >=0.0.3,<0.0.4 when major==minor==0 — npm's caret semantics)
//	"~1.2.3"      tilde range: >=1.2.3, <1.3.0 (>=1.2.0,<1.3.0 if patch omitted)
//
// Versions are plain MAJOR[.MINOR[.PATCH]] (missing components default to 0).
// Pre-release/build metadata suffixes (-rc1, +build) are not supported — this
// is deliberately the minimal matcher S2 needs; a fuller semver library can
// replace this package later without changing the call site in
// internal/app/imports.go.
package kitver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed MAJOR.MINOR.PATCH triple.
type Version struct {
	Major, Minor, Patch int
}

// Compare returns -1, 0, or 1 as v is less than, equal to, or greater than o.
func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		return cmp(v.Major, o.Major)
	case v.Minor != o.Minor:
		return cmp(v.Minor, o.Minor)
	default:
		return cmp(v.Patch, o.Patch)
	}
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func cmp(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// ParseVersion parses a plain "MAJOR[.MINOR[.PATCH]]" string. A leading "v"
// is tolerated (common tag convention, e.g. "v1.2.3"). Missing components
// default to 0.
func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return Version{}, fmt.Errorf("empty version")
	}
	// Strip any pre-release/build suffix (-rc1, +build) — unsupported, but
	// tolerated so a tag like "1.2.3-rc1" at least parses its numeric core.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return Version{}, fmt.Errorf("version %q: too many components", s)
	}
	nums := [3]int{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, fmt.Errorf("version %q: component %q is not numeric", s, p)
		}
		nums[i] = n
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2]}, nil
}

// Satisfies reports whether version satisfies constraint. An empty or "*"
// constraint always matches (including when version itself is empty — a
// kit that declares no app.version: is not rejected merely because the
// importer declared no constraint either).
func Satisfies(version, constraint string) (bool, error) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" || constraint == "*" {
		return true, nil
	}
	v, err := ParseVersion(version)
	if err != nil {
		return false, fmt.Errorf("resolved version %q: %w", version, err)
	}

	op, rest := splitOp(constraint)
	target, err := ParseVersion(rest)
	if err != nil {
		return false, fmt.Errorf("constraint %q: %w", constraint, err)
	}

	switch op {
	case "=":
		return v.Compare(target) == 0, nil
	case ">=":
		return v.Compare(target) >= 0, nil
	case ">":
		return v.Compare(target) > 0, nil
	case "<=":
		return v.Compare(target) <= 0, nil
	case "<":
		return v.Compare(target) < 0, nil
	case "^":
		lo := target
		hi := caretCeiling(target)
		return v.Compare(lo) >= 0 && v.Compare(hi) < 0, nil
	case "~":
		lo := target
		hi := Version{Major: target.Major, Minor: target.Minor + 1, Patch: 0}
		return v.Compare(lo) >= 0 && v.Compare(hi) < 0, nil
	default:
		return false, fmt.Errorf("constraint %q: unrecognised operator %q", constraint, op)
	}
}

// splitOp separates a constraint's leading operator (if any) from its
// version body. No prefix defaults to exact-match "=".
func splitOp(s string) (op, rest string) {
	for _, candidate := range []string{">=", "<=", "^", "~", ">", "<", "="} {
		if strings.HasPrefix(s, candidate) {
			return candidate, strings.TrimSpace(strings.TrimPrefix(s, candidate))
		}
	}
	return "=", s
}

// caretCeiling implements npm's caret semantics: the exclusive upper bound
// bumps the leftmost non-zero component (major, else minor, else patch+1).
func caretCeiling(v Version) Version {
	switch {
	case v.Major > 0:
		return Version{Major: v.Major + 1}
	case v.Minor > 0:
		return Version{Major: 0, Minor: v.Minor + 1}
	default:
		return Version{Major: 0, Minor: 0, Patch: v.Patch + 1}
	}
}
