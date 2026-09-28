package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Docs holds what the Go source says about exported types and their fields,
// keyed "import/path.Type" and "import/path.Type.Field". It is filled by
// generated files (see tools/apidoc), so a comment on a field in the code is
// the same sentence the reference shows.
var docs = struct {
	sync.RWMutex
	m map[string]string
}{m: map[string]string{}}

// AddDocs records comments read from the source.
func AddDocs(m map[string]string) {
	docs.Lock()
	defer docs.Unlock()
	for k, v := range m {
		docs.m[k] = v
	}
}

func docFor(key string) string {
	docs.RLock()
	defer docs.RUnlock()
	return docs.m[key]
}

// Schema is a JSON Schema, as OpenAPI 3.1 uses it.
type Schema = map[string]any

type schemas struct {
	named map[string]Schema
	names map[reflect.Type]string
}

var timeType = reflect.TypeFor[time.Time]()
var rawType = reflect.TypeFor[json.RawMessage]()

func (s *schemas) name(t reflect.Type) string {
	if name, ok := s.names[t]; ok {
		return name
	}
	name := t.Name()
	if _, taken := s.named[name]; taken {
		// A second type of the same name, from another package, is called
		// after its package too, as addon.Page becomes AddonPage.
		pkg := t.PkgPath()[strings.LastIndex(t.PkgPath(), "/")+1:]
		name = strings.ToUpper(pkg[:1]) + pkg[1:] + t.Name()
	}
	return name
}

// of is the schema for a Go type, with named structs referenced from
// components so each is described once.
func (s *schemas) of(t reflect.Type) Schema {
	switch {
	case t == timeType:
		return Schema{"type": "string", "format": "date-time"}
	case t == rawType:
		return Schema{}
	}
	switch t.Kind() {
	case reflect.Pointer:
		inner := s.of(t.Elem())
		return Schema{"anyOf": []any{inner, Schema{"type": "null"}}}
	case reflect.Bool:
		return Schema{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		format := "int64"
		if t.Kind() == reflect.Int32 || t.Kind() == reflect.Uint32 {
			format = "int32"
		}
		return Schema{"type": "integer", "format": format}
	case reflect.Float32, reflect.Float64:
		return Schema{"type": "number"}
	case reflect.String:
		return Schema{"type": "string"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return Schema{"type": "string", "contentEncoding": "base64"}
		}
		return Schema{"type": "array", "items": s.of(t.Elem())}
	case reflect.Map:
		return Schema{"type": "object", "additionalProperties": s.of(t.Elem())}
	case reflect.Interface:
		return Schema{}
	case reflect.Struct:
		if t.Name() == "" {
			return s.object(t)
		}
		if _, ok := s.names[t]; !ok {
			name := s.name(t)
			s.names[t] = name
			s.named[name] = nil
			s.named[name] = s.object(t)
		}
		return Schema{"$ref": "#/components/schemas/" + s.names[t]}
	}
	panic(fmt.Sprintf("api: no schema for %s", t))
}

// jsonField is how encoding/json names a field, and whether it may be left out.
func jsonField(f reflect.StructField) (name string, omitempty, skip bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	return name, slices.Contains(parts[1:], "omitempty") || slices.Contains(parts[1:], "omitzero"), false
}

func (s *schemas) object(t reflect.Type) Schema {
	properties := map[string]any{}
	var order, required []string
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, omitempty, skip := jsonField(f)
			if skip {
				continue
			}
			if f.Anonymous && f.Tag.Get("json") == "" && f.Type.Kind() == reflect.Struct {
				walk(f.Type)
				continue
			}
			field := s.of(f.Type)
			doc := f.Tag.Get("doc")
			if doc == "" && t.Name() != "" {
				doc = docFor(t.PkgPath() + "." + t.Name() + "." + f.Name)
			}
			if doc != "" {
				if _, ref := field["$ref"]; ref {
					field = Schema{"allOf": []any{field}, "description": doc}
				} else {
					field = cloneWith(field, "description", doc)
				}
			}
			if strings.Contains(f.Tag.Get("json"), ",string") {
				field = Schema{"type": "string", "description": field["description"]}
			}
			if _, seen := properties[name]; !seen {
				order = append(order, name)
			}
			properties[name] = field
			if !omitempty {
				required = append(required, name)
			}
		}
	}
	walk(t)
	schema := Schema{"type": "object", "properties": properties, "x-order": order}
	if len(required) > 0 {
		schema["required"] = required
	}
	if t.Name() != "" {
		if doc := docFor(t.PkgPath() + "." + t.Name()); doc != "" {
			schema["description"] = doc
		}
	}
	return schema
}

func cloneWith(s Schema, key string, value any) Schema {
	out := make(Schema, len(s)+1)
	for k, v := range s {
		out[k] = v
	}
	out[key] = value
	return out
}

// Info describes the API as a whole.
type Info struct {
	Title       string
	Version     string
	Description string
}

// OpenAPI is the book as an OpenAPI 3.1 document.
func (b *Book) OpenAPI(info Info) map[string]any {
	b.mu.Lock()
	routes := slices.Clone(b.routes)
	tagDocs := make(map[string]string, len(b.tags))
	for k, v := range b.tags {
		tagDocs[k] = v
	}
	addonNames := make(map[string]string, len(b.addons))
	for k, v := range b.addons {
		addonNames[k] = v
	}
	b.mu.Unlock()
	s := &schemas{named: map[string]Schema{}, names: map[reflect.Type]string{}}
	paths := map[string]map[string]any{}
	var tags []map[string]any
	tagged := map[string]bool{}
	for _, r := range routes {
		if !tagged[r.Tag] {
			tagged[r.Tag] = true
			tag := map[string]any{"name": r.Tag}
			if doc := tagDocs[r.Tag]; doc != "" {
				tag["description"] = doc
			}
			if r.Addon != "" {
				tag["x-cull-addon"] = r.Addon
				if name := addonNames[r.Addon]; name != "" {
					tag["x-cull-addon-name"] = name
				}
			}
			tags = append(tags, tag)
		}
		op := map[string]any{
			"operationId":       r.OperationID(),
			"summary":           r.Summary,
			"tags":              []string{r.Tag},
			"x-cull-permission": r.Needs,
		}
		if r.Doc != "" {
			op["description"] = r.Doc
		}
		if r.Addon != "" {
			op["x-cull-addon"] = r.Addon
		}
		if r.Internal {
			op["x-cull-internal"] = true
		}
		var params []any
		for _, p := range r.Params {
			schema := Schema{"type": p.Type}
			if len(p.Enum) > 0 {
				schema["enum"] = p.Enum
			}
			param := map[string]any{"name": p.Name, "in": p.In, "description": p.Doc, "required": p.Required, "schema": schema}
			if p.Example != "" {
				param["example"] = p.Example
				if p.Type == "integer" {
					if n, err := strconv.ParseInt(p.Example, 10, 64); err == nil {
						param["example"] = n
					}
				}
			}
			params = append(params, param)
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if r.Body != nil {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": s.of(reflect.TypeOf(r.Body))}}}
		}
		responses := map[string]any{}
		ok := map[string]any{"description": "Success"}
		switch {
		case r.Produces != "":
			ok["content"] = map[string]any{r.Produces: map[string]any{"schema": Schema{"type": "string", "format": "binary"}}}
		case r.Returns != nil:
			ok["content"] = map[string]any{"application/json": map[string]any{"schema": s.of(reflect.TypeOf(r.Returns))}}
		}
		responses[strconv.Itoa(r.successStatus())] = ok
		// Every route sits behind the addon guard, and an addon's own route
		// answers 404 while the addon is off, so those are said once here
		// rather than on each route.
		if r.Addon != "" {
			responses["404"] = map[string]any{"description": "The addon this belongs to is turned off"}
		}
		if !r.Internal {
			responses["401"] = map[string]any{"description": "An addon key was sent that Cull does not know"}
		}
		for _, e := range r.Errors {
			responses[strconv.Itoa(e.Status)] = map[string]any{"description": e.When}
		}
		op["responses"] = responses
		if paths[r.Path] == nil {
			paths[r.Path] = map[string]any{}
		}
		paths[r.Path][strings.ToLower(r.Method)] = op
	}
	var permissions []map[string]string
	for _, p := range Permissions {
		permissions = append(permissions, map[string]string{"name": p.Name, "description": p.Doc})
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       info.Title,
			"version":     info.Version,
			"description": info.Description,
		},
		"servers":            []any{map[string]string{"url": "/"}},
		"tags":               tags,
		"paths":              paths,
		"x-cull-api":         Version,
		"x-cull-permissions": permissions,
		"components": map[string]any{
			"schemas": s.named,
			"securitySchemes": map[string]any{
				"addonKey": map[string]any{"type": "http", "scheme": "bearer", "description": "An addon's key, from the key file Cull writes into the addon's folder when it is turned on. Cull's own pages send none."},
			},
		},
		"security": []any{map[string]any{}, map[string]any{"addonKey": []string{}}},
	}
}
