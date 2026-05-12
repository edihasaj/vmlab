// Package schema validates target and flow YAML against JSON schemas embedded
// at build time. Validation errors surface line/path context so a typo in a
// target file fails the command instead of silently producing a partial config.
package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"
)

var msgPrinter = message.NewPrinter(language.English)

//go:embed target.schema.json
var targetSchemaBytes []byte

//go:embed flow.schema.json
var flowSchemaBytes []byte

//go:embed instance.schema.json
var instanceSchemaBytes []byte

var (
	targetSchema   = mustCompile("target", targetSchemaBytes)
	flowSchema     = mustCompile("flow", flowSchemaBytes)
	instanceSchema = mustCompile("instance", instanceSchemaBytes)
)

func mustCompile(name string, data []byte) *jsonschema.Schema {
	c := jsonschema.NewCompiler()
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		panic(fmt.Sprintf("embed %s schema: %v", name, err))
	}
	if err := c.AddResource(name+".schema.json", raw); err != nil {
		panic(err)
	}
	s, err := c.Compile(name + ".schema.json")
	if err != nil {
		panic(err)
	}
	return s
}

// ValidateTarget parses YAML bytes and validates against the target schema.
// path is used for error context only.
func ValidateTarget(path string, data []byte) error {
	return validate(path, data, targetSchema)
}

// ValidateFlow parses YAML bytes and validates against the flow schema.
func ValidateFlow(path string, data []byte) error {
	return validate(path, data, flowSchema)
}

// ValidateInstance parses YAML bytes and validates against the instance schema.
func ValidateInstance(path string, data []byte) error {
	return validate(path, data, instanceSchema)
}

func validate(path string, data []byte, sch *jsonschema.Schema) error {
	// YAML → generic Go map → JSON bytes → schema.
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%s: parse yaml: %w", path, err)
	}
	doc = normalize(doc)
	jb, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("%s: marshal: %w", path, err)
	}
	var decoded any
	if err := json.Unmarshal(jb, &decoded); err != nil {
		return fmt.Errorf("%s: re-decode: %w", path, err)
	}
	if err := sch.Validate(decoded); err != nil {
		return fmt.Errorf("%s:\n%s", path, prettyError(err))
	}
	return nil
}

// normalize converts map[interface{}]interface{} (yaml.v3's default for
// arbitrary keys) into map[string]interface{} so json.Marshal works.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = normalize(v)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[fmt.Sprintf("%v", k)] = normalize(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	default:
		return v
	}
}

func prettyError(err error) string {
	if err == nil {
		return ""
	}
	var ve *jsonschema.ValidationError
	if !asValidationErr(err, &ve) {
		return err.Error()
	}
	var buf bytes.Buffer
	collect(&buf, ve, 0)
	return strings.TrimSpace(buf.String())
}

func collect(buf *bytes.Buffer, ve *jsonschema.ValidationError, depth int) {
	if ve == nil {
		return
	}
	loc := ve.InstanceLocation
	if loc == nil {
		loc = []string{}
	}
	at := strings.Join(loc, ".")
	if at == "" {
		at = "<root>"
	}
	msg := "validation failed"
	if ve.ErrorKind != nil {
		if ls, ok := ve.ErrorKind.(interface {
			LocalizedString(*message.Printer) string
		}); ok {
			msg = ls.LocalizedString(msgPrinter)
		} else if s, ok := ve.ErrorKind.(fmt.Stringer); ok {
			msg = s.String()
		}
	}
	// Only emit a line if this node has its own message or no children (avoid
	// noisy duplicate parents).
	if len(ve.Causes) == 0 {
		fmt.Fprintf(buf, "%s- at %s: %s\n", strings.Repeat("  ", depth), at, msg)
	}
	for _, c := range ve.Causes {
		collect(buf, c, depth)
	}
}

func asValidationErr(err error, target **jsonschema.ValidationError) bool {
	for err != nil {
		if ve, ok := err.(*jsonschema.ValidationError); ok {
			*target = ve
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
