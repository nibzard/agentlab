package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/crypto/ssh"
)

func schemaCompiler(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	payload, err := json.Marshal(buildSchemaResponse())
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft7)
	if err := compiler.AddResource("https://schema.agentlab.invalid/v1", document); err != nil {
		t.Fatal(err)
	}
	return compiler
}
func compileDefinition(t *testing.T, compiler *jsonschema.Compiler, name string) *jsonschema.Schema {
	t.Helper()
	schema, err := compiler.Compile("https://schema.agentlab.invalid/v1#/definitions/" + name)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return schema
}
func jsonValue(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSchemaEveryRequestHasValidatedExamples(t *testing.T) {
	compiler := schemaCompiler(t)
	definitions := schemaDefinitions()
	for name, value := range schemaTypes {
		if !strings.HasSuffix(name, "Request") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			rule, ok := schemaRequestRules()[name]
			if !ok {
				t.Fatalf("no semantic rules/example for request %s", name)
			}
			schema := compileDefinition(t, compiler, name)
			example := jsonValue(t, rule.Example)
			if err := schema.Validate(example); err != nil {
				t.Fatalf("example violates its schema: %v", err)
			}
			payload, _ := json.Marshal(example)
			// The public request decoder is authoritative for wire field/type parity.
			request := reflect.New(reflect.TypeOf(value)).Interface()
			if err := decodeJSONPayload(payload, request); err != nil {
				t.Fatalf("example rejected by actual request decoder: %v", err)
			}
			if key, ok := rule.Example["key"].(string); ok {
				if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key)); err != nil {
					t.Fatalf("invalid SSH public-key example: %v", err)
				}
			}
			definition := definitions[name].(map[string]any)
			if definition["additionalProperties"] != false {
				t.Fatal("strict request decoder is incorrectly described as allowing extra fields")
			}
			if err := schema.Validate(map[string]any{"unknown_field": true}); err == nil {
				t.Fatal("schema accepted unknown request field")
			}
		})
	}
}

func TestSchemaReferencesAndRuntimeAuthorizationParity(t *testing.T) {
	plane := newAuthzPlane(t)
	compiler := schemaCompiler(t)
	identity := &auth.RequestIdentity{Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"catalog.no_permissions"}}}}
	seen := map[string]bool{}
	for _, resource := range schemaResources() {
		for _, name := range []string{resource.RequestType, resource.ResponseType, resource.ErrorType} {
			if name != "" && name != "application/octet-stream" {
				compileDefinition(t, compiler, name)
			}
		}
		for _, variant := range resource.ResponseVariants {
			if variant.Type != "application/octet-stream" {
				compileDefinition(t, compiler, variant.Type)
			}
		}
		for _, method := range resource.Methods {
			key := resource.Surface + " " + method + " " + resource.Path
			if seen[key] {
				t.Errorf("duplicate operation %s", key)
			}
			seen[key] = true
			if resource.Surface != "control" {
				continue
			}
			code, body := plane.serveAuthz(t, identity, method, schemaConcretePath(resource.Path), "")
			if resource.Authorization.Authentication == "none" {
				if code != 200 || string(body) != "ok" {
					t.Errorf("health route: %d %s", code, body)
				}
				continue
			}
			if code != http.StatusForbidden {
				t.Errorf("advertised route %s did not reach authorization: %d %s", key, code, body)
				continue
			}
			var failure V1ErrorResponse
			if err := json.Unmarshal(body, &failure); err != nil {
				t.Fatal(err)
			}
			if permission := resource.Authorization.Permission; permission != "" {
				if failure.Message != "token is not authorized for "+permission && failure.Error != "token is not authorized for "+permission {
					t.Errorf("%s: schema permission %q disagrees with handler: %s", key, permission, body)
				}
			} else if !strings.HasPrefix(resource.Path, "/v1/exec") {
				t.Errorf("missing authorization metadata for %s", key)
			}
		}
	}
	// Every real registered control route family must be represented, including
	// standalone APIs, rather than relying only on a nonempty catalog.
	covered := map[string]bool{}
	for _, resource := range schemaResources() {
		if resource.Surface != "control" {
			continue
		}
		_, pattern := plane.mux.Handler(schemaProbeRequest(&resource))
		if pattern != "" {
			covered[pattern] = true
		}
	}
	registered := registeredPatterns(t, plane.mux)
	if len(registered) < 30 {
		t.Fatal("mux route inventory was not extracted")
	}
	for pattern := range registered {
		if !covered[pattern] {
			t.Errorf("registered public route family absent from schema: %s", pattern)
		}
	}
}

func TestSchemaRuntimeCollectionResponsesValidate(t *testing.T) {
	plane := newAuthzPlane(t)
	compiler := schemaCompiler(t)
	for _, resource := range schemaResources() {
		if resource.Surface != "control" || resource.Methods[0] != http.MethodGet || strings.Contains(resource.Path, "{") || resource.Path == "/v1/messages" {
			continue
		}
		t.Run(resource.Path, func(t *testing.T) {
			code, body := plane.serveAuthz(t, nil, http.MethodGet, resource.Path, "")
			if code != 200 {
				t.Fatalf("collection fixture: %d %s", code, body)
			}
			var value any
			if resource.ResponseType == "V1HealthResponse" {
				value = string(body)
			} else if err := json.Unmarshal(body, &value); err != nil {
				t.Fatal(err)
			}
			if err := compileDefinition(t, compiler, resource.ResponseType).Validate(value); err != nil {
				t.Fatalf("actual response disagrees with schema: %v", err)
			}
		})
	}
}

func TestSchemaAvailabilityTracksConfiguredMuxes(t *testing.T) {
	plane := newAuthzPlane(t)
	response := httptest.NewRecorder()
	plane.api.handleSchema(response, httptest.NewRequest(http.MethodGet, "/v1/schema", nil))
	var catalog schemaResponse
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	foundExec, foundGuest := false, false
	for _, resource := range catalog.Resources {
		if resource.Path == "/v1/exec" {
			foundExec = true
			if resource.Available == nil || !*resource.Available {
				t.Fatal("registered exec route not advertised as configured")
			}
		}
		if resource.Path == "/v1/bootstrap/fetch" {
			foundGuest = true
			if resource.Available == nil || *resource.Available {
				t.Fatal("unregistered guest listener advertised as configured")
			}
		}
	}
	if !foundExec || !foundGuest {
		t.Fatal("availability fixture did not cover both surfaces")
	}
	empty := NewControlAPI(nil, nil, nil, nil, nil, "", nil)
	mux := http.NewServeMux()
	empty.Register(mux)
	response = httptest.NewRecorder()
	empty.handleSchema(response, httptest.NewRequest(http.MethodGet, "/v1/schema", nil))
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	for _, resource := range catalog.Resources {
		if strings.HasPrefix(resource.Path, "/v1/exec") && resource.Available != nil && *resource.Available {
			t.Fatal("disabled exec advertised as available")
		}
	}
}

func TestSchemaRejectsInvalidRequestCombinations(t *testing.T) {
	compiler := schemaCompiler(t)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"V1SandboxCreateRequest", `{}`},
		{"V1SandboxCreateRequest", `{"profile":"default","ttl_minutes":0}`},
		{"V1SandboxCreateRequest", `{"profile":"default","type":"container"}`},
		{"V1SessionCreateRequest", `{"name":"s","profile":"p"}`},
		{"V1SessionCreateRequest", `{"name":"s","profile":"p","workspace_id":"w","workspace_create":{"name":"w","size_gb":10}}`},
		{"V1SandboxUpdateRequest", `{"cores":null}`},
		{"V1ExposureCreateRequest", `{"vmid":1001,"port":65536}`},
		{"V1MessageCreateRequest", `{"scope_type":"all","scope_id":"x"}`},
		{"V1JobCreateRequest", `{"repo_url":"r","profile":"p","task":"t","workspace_id":"w","workspace_create":{"name":"w","size_gb":10}}`},
		{"V1IntegrationCreateRequest", `{"name":"i","secret":"s"}`},
		{"V1IntegrationCreateRequest", `{"name":"i","type":"llm-proxy","target":"https://example.com","provider":"unknown"}`},
		{"ExecRequest", `{"args":[],"command":"  "}`},
	} {
		t.Run(tc.name+tc.body, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tc.body), &value); err != nil {
				t.Fatal(err)
			}
			if err := compileDefinition(t, compiler, tc.name).Validate(value); err == nil {
				t.Fatal("invalid request accepted by schema")
			}
		})
	}
}
