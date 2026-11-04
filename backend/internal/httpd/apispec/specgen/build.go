// Package specgen builds the code-first OpenAPI document from the Go contract
// types. It lives outside apispec because it imports the controllers (to
// reflect their request/response shapes), and controllers import apispec (for
// the 501 stub) — keeping Build here breaks that cycle. apispec only embeds and
// serves the committed openapi.yaml; specgen produces it.
package specgen

import (
	"fmt"
	"reflect"
	"strings"

	jsonschema "github.com/swaggest/jsonschema-go"
	openapi "github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi31"
)

// Build reflects the Go contract types and the operation registry below into
// the OpenAPI document. It is the single source of truth for the /api/v1
// contract: `cmd/genspec` writes its output to apispec/openapi.yaml (the
// committed, embedded artifact) and TestBuild_MatchesEmbedded asserts the embed
// equals fresh Build() output so the two can never drift. Schema facets live as
// struct tags on the service.*/controllers.* types; operation metadata (path,
// status codes, summaries) lives here.
//
// Every wire shape is reflected straight from where it is used at runtime — the
// request bodies, path params, and response envelopes from controllers, the
// error envelope from httpd/envelope — so the served responses and the
// generated schema share one definition each.
func Build() ([]byte, error) {
	r := openapi31.NewReflector()
	// Derive `required` from the idiomatic Go convention: a JSON field without
	// `omitempty` is required. swaggest does not infer this on its own, so the
	// structs stay clean (only description/enum tags) and this hook adds the
	// required array. nonNullableSlices drops the spurious "null" type swaggest
	// stamps on every Go slice.
	r.DefaultOptions = append(r.DefaultOptions,
		func(rc *jsonschema.ReflectContext) { rc.EnvelopNullability = true },
		jsonschema.InterceptProp(requiredFromJSONTag),
		jsonschema.InterceptNullability(nonNullableSlices),
		jsonschema.InterceptNullability(validNullableReferenceUnions),
		// Clean component schema names (which become the generated TS type names):
		// swaggest defaults to PackageType, e.g. "ProjectProject", "EnvelopeAPIError".
		jsonschema.InterceptDefName(schemaName),
	)

	r.Spec.SetTitle("Tallpine HTTP daemon")
	r.Spec.SetVersion("0.1.0-route-shell")
	r.Spec.SetDescription("Loopback-only HTTP surface served by the Go daemon. " +
		"Generated from Go (code-first) — do not edit by hand; run `go generate ./...`.")
	r.Spec.Servers = []openapi31.Server{
		*(&openapi31.Server{URL: "http://127.0.0.1:3101"}).WithDescription("Local daemon (loopback only)"),
	}
	r.Spec.Tags = []openapi31.Tag{
		*(&openapi31.Tag{Name: "agents"}).WithDescription(
			"Supported and locally runnable agent adapters"),
		*(&openapi31.Tag{Name: "projects"}).WithDescription(
			"Project registry, configuration, and lifecycle administration"),
		*(&openapi31.Tag{Name: "sessions"}).WithDescription(
			"Agent session lifecycle and messaging"),
		*(&openapi31.Tag{Name: "automations"}).WithDescription(
			"Recurring daemon-owned session schedules and durable run history"),
		*(&openapi31.Tag{Name: "prs"}).WithDescription(
			"Pull-request actions (SCM lane)"),
		*(&openapi31.Tag{Name: "reviews"}).WithDescription(
			"Code-review runs and findings"),
		*(&openapi31.Tag{Name: "notifications"}).WithDescription(
			"Durable dashboard notifications"),
		*(&openapi31.Tag{Name: "reports"}).WithDescription(
			"Durable worker reports"),
		*(&openapi31.Tag{Name: "usage"}).WithDescription(
			"Token usage telemetry for Tallpine sessions"),
		*(&openapi31.Tag{Name: "push"}).WithDescription(
			"Mobile push-device registration for OS push notifications"),
		*(&openapi31.Tag{Name: "events"}).WithDescription(
			"Server-sent CDC event stream with durable replay"),
		*(&openapi31.Tag{Name: "import"}).WithDescription(
			"Project import onboarding (folder validation and Git preparation)"),
		*(&openapi31.Tag{Name: "dev"}).WithDescription(
			"Developer-only maintenance operations"),
		*(&openapi31.Tag{Name: "mobile"}).WithDescription(
			"Connect Mobile LAN bridge control (loopback/desktop only)"),
		*(&openapi31.Tag{Name: "browser"}).WithDescription(
			"Target-isolated desktop browser runtime (loopback only)"),
		*(&openapi31.Tag{Name: "fs"}).WithDescription(
			"Read-only filesystem browsing for remote clients"),
		*(&openapi31.Tag{Name: "system"}).WithDescription(
			"Local machine readiness checks the desktop app runs before showing the board"),
		*(&openapi31.Tag{Name: "link-preview"}).WithDescription(
			"Server-side unfurl of external links for the CSP-locked renderer"),
		*(&openapi31.Tag{Name: "github"}).WithDescription(
			"Local GitHub personal access token storage and repository listing"),
	}

	for _, op := range operations() {
		oc, err := r.NewOperationContext(op.method, op.path)
		if err != nil {
			return nil, fmt.Errorf("new operation %s %s: %w", op.method, op.path, err)
		}
		oc.SetID(op.id)
		oc.SetSummary(op.summary)
		oc.SetTags(op.tag)
		for _, param := range op.pathParams {
			oc.AddReqStructure(param)
		}
		if op.reqBody != nil {
			// AddReqStructure leaves requestBody.required absent, which
			// OpenAPI reads as optional. Most of these bodies are mandatory, so
			// force it — otherwise validators/generators treat the body as
			// skippable. Ops that genuinely accept an empty body opt out.
			if op.optionalReqBody {
				oc.AddReqStructure(op.reqBody)
			} else {
				oc.AddReqStructure(op.reqBody, openapi.WithCustomize(markRequestBodyRequired))
			}
		}
		for _, resp := range op.resps {
			opts := []openapi.ContentOption{openapi.WithHTTPStatus(resp.status)}
			if op.contentTypes != nil && op.contentTypes[resp.status] != "" {
				opts = append(opts, openapi.WithContentType(op.contentTypes[resp.status]))
			}
			oc.AddRespStructure(resp.body, opts...)
		}
		if err := r.AddOperation(oc); err != nil {
			return nil, fmt.Errorf("add operation %s %s: %w", op.method, op.path, err)
		}
	}

	return r.Spec.MarshalYAML()
}

// schemaName maps swaggest's default PackageType component names (e.g.
// "ProjectProject", "EnvelopeAPIError") to the clean, stable schema names that
// become the generated TypeScript type names. Every reflected type is listed
// explicitly: an unrecognised default name is returned verbatim, so a new type
// surfaces as a visibly-wrong "PackageType" name in the diff (and the drift
// test) rather than silently colliding with an existing schema via a
// TrimPrefix catch-all.
func schemaName(_ reflect.Type, defaultName string) string {
	if clean, ok := schemaNames[defaultName]; ok {
		return clean
	}
	// Types shared with the CLI live in httpd/apitypes; their clean name is the
	// bare type name. A collision with an existing schema fails the drift test.
	return strings.TrimPrefix(defaultName, "Apitypes")
}

// markRequestBodyRequired sets requestBody.required: true on the operation's
// JSON body. swaggest leaves it absent (== optional) for AddReqStructure bodies.
func markRequestBodyRequired(cor openapi.ContentOrReference) {
	if rb, ok := cor.(*openapi31.RequestBodyOrReference); ok && rb.RequestBody != nil {
		rb.RequestBody.WithRequired(true)
	}
}

// nonNullableSlices drops the "null" that swaggest unions into every Go slice
// type (a nil slice marshals as JSON null). A required array field should be
// `T[]`, not `T[] | null`; the handlers normalise nil to an empty slice, so
// null never reaches the wire. Byte slices (base64 strings) are left alone.
func nonNullableSlices(p jsonschema.InterceptNullabilityParams) {
	if !p.NullAdded || p.Type == nil || p.Type.Kind() != reflect.Slice {
		return
	}
	if p.Type.Elem().Kind() == reflect.Uint8 {
		return
	}
	p.Schema.TypeEns().WithSimpleTypes(jsonschema.Array)
	p.Schema.Type.SliceOfSimpleTypeValues = nil
}

// validNullableReferenceUnions removes the original concrete type left beside
// an enveloped null-or-reference anyOf. Keeping that sibling type would apply
// it conjunctively and reject null despite the explicit null branch.
func validNullableReferenceUnions(p jsonschema.InterceptNullabilityParams) {
	if p.Schema.Type == nil || len(p.Schema.AnyOf) == 0 {
		return
	}
	for _, option := range p.Schema.AnyOf {
		if option.TypeObject != nil && option.TypeObject.HasType(jsonschema.Null) {
			p.Schema.Type = nil
			return
		}
	}
}

// requiredFromJSONTag marks a property required when its json tag lacks
// `omitempty` (the Go convention for "always present"). Runs after default
// processing so ParentSchema exists; skips fields without a json tag (e.g. path
// params, which swaggest marks required on their own).
func requiredFromJSONTag(p jsonschema.InterceptPropParams) error {
	if !p.Processed || p.ParentSchema == nil {
		return nil
	}
	jsonTag := p.Field.Tag.Get("json")
	if jsonTag == "" || jsonTag == "-" {
		return nil
	}
	parts := strings.Split(jsonTag, ",")
	name := parts[0]
	if name == "" {
		name = p.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			return nil
		}
	}
	for _, existing := range p.ParentSchema.Required {
		if existing == name {
			return nil
		}
	}
	p.ParentSchema.Required = append(p.ParentSchema.Required, name)
	return nil
}

// --- operation registry -----------------------------------------------------

type respUnit struct {
	status int
	body   any
}

type operation struct {
	method, path, id, summary string
	tag                       string
	pathParams                []any // path/query param containers (e.g. ProjectIDParam)
	reqBody                   any   // JSON request body struct, nil when the op takes none
	// optionalReqBody declares the body without marking it required, for the
	// handlers that accept an empty body as a meaningful default.
	optionalReqBody bool
	resps           []respUnit
	contentTypes    map[int]string // optional non-JSON response content types by status
}

func operations() []operation {
	ops := append([]operation{}, eventOperations()...)
	ops = append(ops, agentOperations()...)
	ops = append(ops, projectOperations()...)
	ops = append(ops, sessionOperations()...)
	ops = append(ops, automationOperations()...)
	ops = append(ops, prOperations()...)
	ops = append(ops, reviewOperations()...)
	ops = append(ops, notificationOperations()...)
	ops = append(ops, reportOperations()...)
	ops = append(ops, usageOperations()...)
	ops = append(ops, pushOperations()...)
	ops = append(ops, importOperations()...)
	ops = append(ops, fsOperations()...)
	ops = append(ops, devOperations()...)
	ops = append(ops, mobileOperations()...)
	ops = append(ops, awakeOperations()...)
	ops = append(ops, mobileDeviceOperations()...)
	ops = append(ops, browserOperations()...)
	ops = append(ops, shellTerminalOperations()...)
	ops = append(ops, cueOperations()...)
	ops = append(ops, backlogOperations()...)
	ops = append(ops, backlogRunOperations()...)
	ops = append(ops, backlogPlanOperations()...)
	ops = append(ops, systemOperations()...)
	ops = append(ops, identityOperations()...)
	ops = append(ops, endpointsOperations()...)
	ops = append(ops, linkPreviewOperations()...)
	return ops
}
