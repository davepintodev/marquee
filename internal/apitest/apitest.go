// Package apitest checks live HTTP responses against api/openapi.yaml, so
// the fake and the real service are held to the same contract.
package apitest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/davepintodev/marquee/api"
)

func init() {
	passthrough := func(body io.Reader, _ http.Header, _ *openapi3.SchemaRef, _ openapi3filter.EncodingFn) (any, error) {
		b, err := io.ReadAll(body)
		return string(b), err
	}
	openapi3filter.RegisterBodyDecoder("image/svg+xml", passthrough)
	openapi3filter.RegisterBodyDecoder("image/jpeg", passthrough)
	openapi3filter.RegisterBodyDecoder("image/png", passthrough)
	openapi3filter.RegisterBodyDecoder("image/webp", passthrough)
}

// LoadSpec parses and validates the contract and returns a router over it.
func LoadSpec(t *testing.T) (*openapi3.T, routers.Router) {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("openapi.yaml is not valid: %v", err)
	}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	return doc, router
}

// Validate checks that the request is one the contract allows and that the
// response, read to its end here, has a status, headers and body the
// contract declares for it.
func Validate(t *testing.T, router routers.Router, method, path, body string, resp *http.Response, status int) {
	t.Helper()
	req, _ := http.NewRequest(method, "http://example.test"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	route, params, err := router.FindRoute(req)
	if err != nil {
		t.Fatalf("%s %s is not in the contract: %v", method, path, err)
	}
	in := &openapi3filter.RequestValidationInput{
		Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
	if err := openapi3filter.ValidateRequest(context.Background(), in); err != nil {
		t.Fatalf("request does not match the contract: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	out := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in, Status: status, Header: resp.Header,
		Options: &openapi3filter.Options{IncludeResponseStatus: true},
	}
	out.SetBodyBytes(raw)
	if err := openapi3filter.ValidateResponse(context.Background(), out); err != nil {
		t.Fatalf("%s %s: %d response does not match the contract: %v\nbody: %s", method, path, status, err, raw)
	}
}
