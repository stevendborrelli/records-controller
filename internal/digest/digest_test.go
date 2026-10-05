package digest

import (
	"strings"
	"testing"
)

func TestCanonicalize(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"SortsKeys":            {`{"b":1,"a":2}`, `{"a":2,"b":1}`},
		"NestedAndWhitespace":  {"{ \"z\": [ 1, {\"y\":true, \"x\":null} ] }", `{"z":[1,{"x":null,"y":true}]}`},
		"Integer":              {`100`, `100`},
		"TrailingZeroFraction": {`1.0`, `1`},
		"NegativeZero":         {`-0`, `0`},
		"SmallDecimal":         {`0.000001`, `0.000001`},
		"SmallExponent":        {`0.0000001`, `1e-7`},
		"LargeInteger":         {`123456789012345680000`, `123456789012345680000`},
		"LargeExponent":        {`1e21`, `1e+21`},
		"Fraction":             {`-1.5e-3`, `-0.0015`},
		"StringEscapes":        {`"a\"b\\c\n\u0001"`, `"a\"b\\c\n\u0001"`},
		"NoUnicodeEscaping":    {`"€"`, `"€"`},
		"NoHTMLEscaping":       {`"<&>"`, `"<&>"`},
		// RFC 8785 sorts by UTF-16 code units: U+FB33 sorts after U+1F600,
		// whose UTF-16 form starts with the surrogate 0xD83D.
		"UTF16KeyOrder": {`{"\ufb33":1,"\ud83d\ude00":2}`, "{\"\U0001F600\":2,\"\uFB33\":1}"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Canonicalize([]byte(tc.in))
			if err != nil {
				t.Fatalf("Canonicalize(%s): %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Errorf("Canonicalize(%s): got %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestCanonicalizeRejectsInvalid(t *testing.T) {
	for _, in := range []string{`{`, `{"a":1} {}`, `1e400`} {
		if _, err := Canonicalize([]byte(in)); err == nil {
			t.Errorf("Canonicalize(%s): want error", in)
		}
	}
}

func TestOfIsRepresentationIndependent(t *testing.T) {
	a, err := Of([]byte(`{"cidr":"10.21.0.0/16","zones":["a","b"]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Of([]byte("{\n  \"zones\": [\"a\", \"b\"],\n  \"cidr\": \"10.21.0.0/16\"\n}"))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("equivalent JSON produced different digests: %s != %s", a, b)
	}
	if !strings.HasPrefix(a, Prefix) || len(a) != len(Prefix)+64 {
		t.Errorf("malformed digest %q", a)
	}
}
