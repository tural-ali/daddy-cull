package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Conforms checks a JSON response against the schema the reference gives
// for it, so a test can prove that what a route returns is what its
// documentation says. It understands the subset of JSON Schema that
// OpenAPI generates here.
func Conforms(document map[string]any, route Route, body []byte) error {
	paths, _ := document["paths"].(map[string]map[string]any)
	op, _ := paths[route.Path][strings.ToLower(route.Method)].(map[string]any)
	if op == nil {
		return fmt.Errorf("%s is not in the document", route.Pattern())
	}
	responses, _ := op["responses"].(map[string]any)
	var schema Schema
	for status, response := range responses {
		if !strings.HasPrefix(status, "2") {
			continue
		}
		content, _ := response.(map[string]any)["content"].(map[string]any)
		if media, ok := content["application/json"].(map[string]any); ok {
			schema, _ = media["schema"].(Schema)
		}
	}
	if schema == nil {
		return fmt.Errorf("%s documents no JSON response", route.Pattern())
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return err
	}
	components, _ := document["components"].(map[string]any)
	named, _ := components["schemas"].(map[string]Schema)
	return conforms(named, schema, value, "$")
}

func conforms(named map[string]Schema, schema Schema, value any, at string) error {
	if ref, ok := schema["$ref"].(string); ok {
		target, found := named[strings.TrimPrefix(ref, "#/components/schemas/")]
		if !found {
			return fmt.Errorf("%s: unknown %s", at, ref)
		}
		return conforms(named, target, value, at)
	}
	if all, ok := schema["allOf"].([]any); ok {
		for _, part := range all {
			if err := conforms(named, part.(Schema), value, at); err != nil {
				return err
			}
		}
		return nil
	}
	if any, ok := schema["anyOf"].([]any); ok {
		var first error
		for _, part := range any {
			err := conforms(named, part.(Schema), value, at)
			if err == nil {
				return nil
			}
			if first == nil {
				first = err
			}
		}
		return first
	}
	typ, _ := schema["type"].(string)
	switch typ {
	case "":
		return nil
	case "null":
		if value != nil {
			return fmt.Errorf("%s: expected null, got %T", at, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: expected a boolean, got %s", at, describe(value))
		}
	case "integer", "number":
		n, ok := value.(float64)
		if !ok {
			return fmt.Errorf("%s: expected a number, got %s", at, describe(value))
		}
		if typ == "integer" && n != float64(int64(n)) {
			return fmt.Errorf("%s: expected an integer, got %v", at, n)
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: expected a string, got %s", at, describe(value))
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: expected an array, got %s", at, describe(value))
		}
		for i, item := range items {
			if err := conforms(named, schema["items"].(Schema), item, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected an object, got %s", at, describe(value))
		}
		properties, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]string)
		for _, name := range required {
			if _, ok := object[name]; !ok {
				return fmt.Errorf("%s: %s is missing", at, name)
			}
		}
		extra, _ := schema["additionalProperties"].(Schema)
		for name, field := range object {
			if property, ok := properties[name].(Schema); ok {
				if err := conforms(named, property, field, at+"."+name); err != nil {
					return err
				}
			} else if extra != nil {
				if err := conforms(named, extra, field, at+"."+name); err != nil {
					return err
				}
			} else if properties != nil {
				known := make([]string, 0, len(properties))
				for k := range properties {
					known = append(known, k)
				}
				slices.Sort(known)
				return fmt.Errorf("%s: %s is not documented (documented: %s)", at, name, strings.Join(known, ", "))
			}
		}
	default:
		return fmt.Errorf("%s: unknown type %q", at, typ)
	}
	return nil
}

func describe(value any) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%T %v", value, value)
}
