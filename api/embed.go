// Package api embeds the OpenAPI description, the contract the web UI,
// the backend and the fake all follow.
package api

import _ "embed"

//go:embed openapi.yaml
var Spec []byte
