// Package validate validates Record data against a StructuralSchema contract.
package validate

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// A Contract is a compiled StructuralSchema definition.
type Contract struct {
	structural *structuralschema.Structural
	validator  validation.SchemaValidator
}

// Compile compiles a StructuralSchema definition. It returns an error if the
// definition is not a valid structural schema with an object root.
func Compile(definition []byte) (*Contract, error) {
	v1 := &apiextensionsv1.JSONSchemaProps{}
	if err := json.Unmarshal(definition, v1); err != nil {
		return nil, fmt.Errorf("cannot parse definition: %w", err)
	}
	if v1.Type != "object" {
		return nil, fmt.Errorf("definition must have type: object at its root")
	}
	internal := &apiextensions.JSONSchemaProps{}
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(v1, internal, nil); err != nil {
		return nil, fmt.Errorf("cannot convert definition: %w", err)
	}
	s, err := structuralschema.NewStructural(internal)
	if err != nil {
		return nil, fmt.Errorf("definition is not a structural schema: %w", err)
	}
	if errs := structuralschema.ValidateStructural(field.NewPath("definition"), s); len(errs) > 0 {
		return nil, fmt.Errorf("definition is not a structural schema: %w", errs.ToAggregate())
	}
	sv, _, err := validation.NewSchemaValidator(internal)
	if err != nil {
		return nil, fmt.Errorf("cannot build validator: %w", err)
	}
	return &Contract{structural: s, validator: sv}, nil
}

// Validate validates a JSON object against the contract. Fields the contract
// does not allow are an error: pruning them would change the data, and
// therefore its digest.
func (c *Contract) Validate(data []byte) error {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("data must be a JSON object: %w", err)
	}

	// Pruning mutates its input, so prune a copy and only use it to find
	// unknown fields.
	cp := runtime.DeepCopyJSONValue(obj)
	unknown := pruning.PruneWithOptions(cp, c.structural, false, structuralschema.UnknownFieldPathOptions{TrackUnknownFieldPaths: true})
	if len(unknown) > 0 {
		return fmt.Errorf("data contains fields not allowed by the schema: %s", strings.Join(unknown, ", "))
	}

	if errs := validation.ValidateCustomResource(field.NewPath("data"), obj, c.validator); len(errs) > 0 {
		return errs.ToAggregate()
	}
	return nil
}
