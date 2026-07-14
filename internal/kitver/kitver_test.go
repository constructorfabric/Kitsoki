package kitver

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want Version
	}{
		{"1.2.3", Version{1, 2, 3}},
		{"v1.2.3", Version{1, 2, 3}},
		{"1.2", Version{1, 2, 0}},
		{"1", Version{1, 0, 0}},
		{"1.2.3-rc1", Version{1, 2, 3}},
	}
	for _, c := range cases {
		got, err := ParseVersion(c.in)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseVersion(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseVersionErrors(t *testing.T) {
	for _, in := range []string{"", "a.b.c", "1.2.3.4"} {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q): expected error, got nil", in)
		}
	}
}

func TestSatisfies(t *testing.T) {
	cases := []struct {
		version, constraint string
		want                bool
	}{
		{"1.2.3", "", true},
		{"1.2.3", "*", true},
		{"1.2.3", "1.2.3", true},
		{"1.2.4", "1.2.3", false},
		{"1.2.3", "=1.2.3", true},
		{"1.3.0", ">=1.2.3", true},
		{"1.2.0", ">=1.2.3", false},
		{"1.2.3", ">1.2.3", false},
		{"1.2.4", ">1.2.3", true},
		{"1.2.3", "<=1.2.3", true},
		{"1.2.4", "<=1.2.3", false},
		{"1.2.2", "<1.2.3", true},
		// caret: >=1.2.3 <2.0.0
		{"1.2.3", "^1.2.3", true},
		{"1.9.9", "^1.2.3", true},
		{"2.0.0", "^1.2.3", false},
		{"1.2.2", "^1.2.3", false},
		// caret with major==0: >=0.2.3 <0.3.0
		{"0.2.3", "^0.2.3", true},
		{"0.2.9", "^0.2.3", true},
		{"0.3.0", "^0.2.3", false},
		// caret with major==minor==0: >=0.0.3 <0.0.4
		{"0.0.3", "^0.0.3", true},
		{"0.0.4", "^0.0.3", false},
		// tilde: >=1.2.3 <1.3.0
		{"1.2.9", "~1.2.3", true},
		{"1.3.0", "~1.2.3", false},
		{"1.2.0", "~1.2.3", false},
	}
	for _, c := range cases {
		got, err := Satisfies(c.version, c.constraint)
		if err != nil {
			t.Fatalf("Satisfies(%q, %q): %v", c.version, c.constraint, err)
		}
		if got != c.want {
			t.Errorf("Satisfies(%q, %q) = %v, want %v", c.version, c.constraint, got, c.want)
		}
	}
}

func TestSatisfiesErrors(t *testing.T) {
	if _, err := Satisfies("not-a-version", "^1.0.0"); err == nil {
		t.Error("expected error for unparseable version")
	}
	if _, err := Satisfies("1.0.0", "??1.0.0"); err == nil {
		t.Error("expected error for malformed constraint")
	}
}
