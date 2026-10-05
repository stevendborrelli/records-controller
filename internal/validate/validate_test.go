package validate

import (
	"strings"
	"testing"
)

const subnetV1 = `{
  "type": "object",
  "required": ["cidr"],
  "properties": {
    "cidr": {"type": "string"},
    "location": {
      "type": "object",
      "properties": {
        "region": {"type": "string"},
        "country": {"type": "string"}
      }
    },
    "region": {"type": "string"},
    "zones": {"type": "array", "items": {"type": "string"}},
    "gateway": {"type": "string"}
  }
}`

func TestValidate(t *testing.T) {
	c, err := Compile([]byte(subnetV1))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	cases := map[string]struct {
		data    string
		wantErr string
	}{
		"Valid": {
			data: `{"cidr":"10.21.0.0/16","location":{"region":"NorthAmerica","country":"UnitedStates"},"zones":["us-west-2a"]}`,
		},
		"WrongType": {
			data:    `{"cidr":10}`,
			wantErr: "data.cidr",
		},
		"MissingRequired": {
			data:    `{"region":"us-west-2"}`,
			wantErr: "cidr",
		},
		"UnknownField": {
			data:    `{"cidr":"10.0.0.0/8","owner":"team-a"}`,
			wantErr: "not allowed by the schema: owner",
		},
		"UnknownNestedField": {
			data:    `{"cidr":"10.0.0.0/8","location":{"planet":"Earth"}}`,
			wantErr: "location.planet",
		},
		"NotAnObject": {
			data:    `["a"]`,
			wantErr: "must be a JSON object",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := c.Validate([]byte(tc.data))
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("Validate: unexpected error: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Errorf("Validate: want error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("Validate: want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestCompileRejectsInvalidDefinitions(t *testing.T) {
	cases := map[string]string{
		"NotObjectRoot":  `{"type":"string"}`,
		"NotStructural":  `{"type":"object","properties":{"a":{}}}`,
		"MalformedJSON":  `{"type":`,
		"InvalidPattern": `{"type":"object","properties":{"a":{"type":"string","pattern":"("}}}`,
	}
	for name, def := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Compile([]byte(def)); err == nil {
				t.Errorf("Compile(%s): want error", def)
			}
		})
	}
}
