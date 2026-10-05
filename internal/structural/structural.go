// Package structural removes documentation-only keywords from a
// StructuralSchema definition, so that definitions differing only in
// documentation have the same structural digest.
package structural

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// documentation is the set of keywords Records v1alpha1 defines as
// documentation-only. No other keyword may be removed.
var documentation = map[string]bool{
	"description":  true,
	"title":        true,
	"example":      true,
	"externalDocs": true,
}

// Strip returns a definition with the documentation-only keywords removed
// from every schema node.
//
// A keyword is removed only where it is a keyword: at the root, or in a
// schema nested under another keyword that holds schemas. The same names are
// kept everywhere else, because there they are data: a property named
// description, a key inside default or enum, or a field of an x-kubernetes-*
// extension.
func Strip(definition []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(definition))
	d.UseNumber() // Keep numbers exactly as written.
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, fmt.Errorf("cannot parse definition: %w", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after definition")
	}
	return json.Marshal(schema(v))
}

// schema strips a schema node. Anything that is not an object, such as the
// boolean form of additionalProperties, is returned unchanged.
func schema(v any) any {
	node, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(node))
	for k, val := range node {
		switch {
		case documentation[k]:
			continue
		case k == "properties" || k == "patternProperties" || k == "definitions" || k == "dependencies":
			// Maps from a name to a schema. A dependencies value may
			// instead be a list of property names, which schema leaves
			// unchanged.
			out[k] = schemaMap(val)
		case k == "items":
			// A schema, or a list of schemas.
			if l, ok := val.([]any); ok {
				out[k] = schemaList(l)
			} else {
				out[k] = schema(val)
			}
		case k == "allOf" || k == "anyOf" || k == "oneOf":
			if l, ok := val.([]any); ok {
				out[k] = schemaList(l)
			} else {
				out[k] = val
			}
		case k == "not" || k == "additionalProperties" || k == "additionalItems":
			out[k] = schema(val)
		default:
			// Everything else is a value, not a schema: type, required,
			// default, enum, x-kubernetes-validations, and so on.
			out[k] = val
		}
	}
	return out
}

func schemaMap(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m))
	for name, s := range m {
		out[name] = schema(s)
	}
	return out
}

func schemaList(l []any) []any {
	out := make([]any, len(l))
	for i, s := range l {
		out[i] = schema(s)
	}
	return out
}
