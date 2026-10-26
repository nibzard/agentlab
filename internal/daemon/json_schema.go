package daemon

import (
	"encoding/json"
	"reflect"
	"strings"
)

// schemaDefinitions reflects wire tags, preserving nested definitions. Request
// required fields are set by validation rules rather than Go zero values.
func schemaDefinitions() map[string]any {
	defs := make(map[string]any)
	for name, value := range schemaTypes {
		schema := schemaType(reflect.TypeOf(value), defs)
		if _, exists := defs[name]; !exists {
			defs[name] = schema
		}
	}
	applySchemaRequestRules(defs)
	return defs
}
func schemaType(t reflect.Type, defs map[string]any) map[string]any {
	if t.Kind() == reflect.Pointer {
		return nullableSchema(schemaType(t.Elem(), defs))
	}
	if t == reflect.TypeOf(json.RawMessage{}) {
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.Struct:
		if t.PkgPath() == "time" && t.Name() == "Time" {
			return map[string]any{"type": "string", "format": "date-time"}
		}
		name := t.Name()
		if name != "" {
			if _, ok := defs[name]; ok {
				return map[string]any{"$ref": "#/definitions/" + name}
			}
			defs[name] = map[string]any{}
		}
		props := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if tag == "-" {
				continue
			}
			if tag == "" {
				tag = field.Name
			}
			props[tag] = schemaType(field.Type, defs)
		}
		schema := map[string]any{"type": "object", "properties": props, "additionalProperties": !strings.HasSuffix(name, "Request")}
		if !strings.HasSuffix(name, "Request") {
			required := make([]string, 0)
			for i := 0; i < t.NumField(); i++ {
				field := t.Field(i)
				tag := field.Tag.Get("json")
				if !field.IsExported() || tag == "-" || strings.Contains(tag, ",omitempty") {
					continue
				}
				key := strings.Split(tag, ",")[0]
				if key == "" {
					key = field.Name
				}
				required = append(required, key)
			}
			if len(required) > 0 {
				schema["required"] = required
			}
		}
		if name == "" {
			return schema
		}
		defs[name] = schema
		return map[string]any{"$ref": "#/definitions/" + name}
	case reflect.Slice, reflect.Array:
		return nullableSchema(map[string]any{"type": "array", "items": schemaType(t.Elem(), defs)})
	case reflect.Map:
		return nullableSchema(map[string]any{"type": "object", "additionalProperties": schemaType(t.Elem(), defs)})
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	default:
		return map[string]any{}
	}
}

func nullableSchema(schema map[string]any) map[string]any {
	return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
}
