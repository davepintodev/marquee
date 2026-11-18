package importer_test

import (
	"net/http"
	"net/http/httptest"
)

func newHangingServer(release <-chan struct{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
}
