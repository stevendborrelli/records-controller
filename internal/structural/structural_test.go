package structural

import (
	"testing"

	"github.com/stevendborrelli/records-controller/internal/digest"
)

func TestStrip(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"RemovesDocumentationAtRoot": {
			in:   `{"type":"object","description":"a subnet","title":"Subnet","example":{"cidr":"10.0.0.0/16"},"externalDocs":{"url":"https://example.org"}}`,
			want: `{"type":"object"}`,
		},
		"KeepsOtherKeywords": {
			in:   `{"type":"object","required":["cidr"],"x-kubernetes-preserve-unknown-fields":true,"properties":{"cidr":{"type":"string","pattern":"^[0-9./]+$","maxLength":18}}}`,
			want: `{"type":"object","required":["cidr"],"x-kubernetes-preserve-unknown-fields":true,"properties":{"cidr":{"type":"string","pattern":"^[0-9./]+$","maxLength":18}}}`,
		},
		"RemovesDocumentationFromNestedSchemas": {
			in: `{"type":"object","properties":{
				"zones":{"type":"array","description":"zones","items":{"type":"string","title":"zone"}},
				"tags":{"type":"object","additionalProperties":{"type":"string","description":"tag value"}},
				"labels":{"type":"object","patternProperties":{"^a":{"type":"string","example":"a1"}}},
				"either":{"anyOf":[{"type":"string","description":"s"}],"oneOf":[{"type":"integer","title":"i"}],"allOf":[{"externalDocs":{}}],"not":{"type":"boolean","description":"b"}}
			}}`,
			want: `{"type":"object","properties":{
				"zones":{"type":"array","items":{"type":"string"}},
				"tags":{"type":"object","additionalProperties":{"type":"string"}},
				"labels":{"type":"object","patternProperties":{"^a":{"type":"string"}}},
				"either":{"anyOf":[{"type":"string"}],"oneOf":[{"type":"integer"}],"allOf":[{}],"not":{"type":"boolean"}}
			}}`,
		},
		"KeepsPropertiesNamedLikeDocumentation": {
			in:   `{"type":"object","properties":{"description":{"type":"string","description":"free text"},"title":{"type":"string"},"example":{"type":"string"},"externalDocs":{"type":"string"}}}`,
			want: `{"type":"object","properties":{"description":{"type":"string"},"title":{"type":"string"},"example":{"type":"string"},"externalDocs":{"type":"string"}}}`,
		},
		"KeepsDocumentationNamesInsideValues": {
			in:   `{"type":"object","default":{"description":"d","title":"t"},"enum":[{"example":1}],"x-kubernetes-validations":[{"rule":"has(self.description)","message":"description is required"}]}`,
			want: `{"type":"object","default":{"description":"d","title":"t"},"enum":[{"example":1}],"x-kubernetes-validations":[{"rule":"has(self.description)","message":"description is required"}]}`,
		},
		"ItemsAsAList": {
			in:   `{"type":"array","items":[{"type":"string","description":"first"},{"type":"integer","title":"second"}],"additionalItems":{"type":"boolean","example":true}}`,
			want: `{"type":"array","items":[{"type":"string"},{"type":"integer"}],"additionalItems":{"type":"boolean"}}`,
		},
		"BooleanAdditionalProperties": {
			in:   `{"type":"object","additionalProperties":false}`,
			want: `{"type":"object","additionalProperties":false}`,
		},
		"DependenciesSchemaAndList": {
			in:   `{"type":"object","dependencies":{"a":["b","description"],"c":{"type":"object","description":"x"}}}`,
			want: `{"type":"object","dependencies":{"a":["b","description"],"c":{"type":"object"}}}`,
		},
		"NumbersAreUnchanged": {
			in:   `{"type":"number","maximum":12345678901234567890,"multipleOf":0.1}`,
			want: `{"type":"number","maximum":12345678901234567890,"multipleOf":0.1}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Strip([]byte(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if g, w := canonical(t, got), canonical(t, []byte(tc.want)); g != w {
				t.Errorf("Strip:\n got %s\nwant %s", g, w)
			}
		})
	}
}

func TestStripRejectsInvalidJSON(t *testing.T) {
	for _, in := range []string{`{"type":`, `{"type":"object"} {}`} {
		if _, err := Strip([]byte(in)); err == nil {
			t.Errorf("Strip(%s): want an error", in)
		}
	}
}

// Definitions differing only in documentation strip to the same document.
func TestStripIgnoresDocumentationOnly(t *testing.T) {
	gb := `{"type":"object","properties":{"size":{"type":"integer","description":"size in GB"}}}`
	mb := `{"type":"object","properties":{"size":{"type":"integer","description":"size in MB"}}}`
	a, err := Strip([]byte(gb))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Strip([]byte(mb))
	if err != nil {
		t.Fatal(err)
	}
	if canonical(t, a) != canonical(t, b) {
		t.Errorf("documentation-only change is visible after Strip: %s vs %s", a, b)
	}
}

func canonical(t *testing.T, doc []byte) string {
	t.Helper()
	c, err := digest.Canonicalize(doc)
	if err != nil {
		t.Fatalf("cannot canonicalize %s: %v", doc, err)
	}
	return string(c)
}
