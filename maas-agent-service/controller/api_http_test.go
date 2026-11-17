package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	jwt "github.com/golang-jwt/jwt/v5"
	fiberserver "github.com/netcracker/qubership-core-lib-go-fiber-server-utils/v3"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/httputils"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testNamespace = "test-namespace"

func TestProxiedRequest(t *testing.T) {
	upstream := &recordingUpstream{}
	app := newHttpApp(t, upstream)
	tests := []struct {
		name            string
		method          string
		target          string
		body            string
		wantUpstreamURI string
	}{
		{name: "GET with query string", method: http.MethodGet, target: "/api/v1/topic?name=a&onTopicExists=merge", wantUpstreamURI: "/api/v1/topic?name=a&onTopicExists=merge"},
		{name: "POST with body", method: http.MethodPost, target: "/api/v2/topic", body: `{"name":"a"}`, wantUpstreamURI: "/api/v2/topic"},
		{name: "DELETE", method: http.MethodDelete, target: "/api/v1/topic/a", wantUpstreamURI: "/api/v1/topic/a"},
		{name: "dot-dot segment", method: http.MethodGet, target: "/api/v1/x/../topic", wantUpstreamURI: "/api/v1/topic"},
		{name: "encoded dot-dot segment", method: http.MethodGet, target: "/api/v1/x/%2e%2e/topic", wantUpstreamURI: "/api/v1/topic"},
		{name: "percent-encoded unreserved character", method: http.MethodGet, target: "/api/v1/%74opic", wantUpstreamURI: "/api/v1/topic"},
		{name: "percent-encoded reserved character", method: http.MethodGet, target: "/api/v1/topic/a%20b", wantUpstreamURI: "/api/v1/topic/a%2520b"},
		{name: "api-version", method: http.MethodGet, target: "/api-version", wantUpstreamURI: "/api-version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream.reset()
			req := httptest.NewRequest(tt.method, tt.target, strings.NewReader(tt.body))
			req.Header.Set("X-Request-Id", "req-1")
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			status, contentType, body := send(t, app, req)

			received := upstream.last()
			assert.Equal(t, http.StatusCreated, status, "status, body %q", body)
			assert.Equal(t, "application/json", contentType, "Content-Type")
			assert.Equal(t, `{"proxied":true}`, body, "body")
			assert.Equal(t, tt.method, received.method, "method the upstream received")
			assert.Equal(t, tt.wantUpstreamURI, received.requestURI, "request URI the upstream received")
			assert.Equal(t, tt.body, received.body, "body the upstream received")
			assert.Equal(t, "req-1", received.requestID, "X-Request-Id the upstream received")
			assert.Equal(t, testNamespace, received.originNamespace, "X-Origin-Namespace the upstream received")
		})
	}
}

func TestProxyErrorResponse(t *testing.T) {
	app := newHttpApp(t, &recordingUpstream{})

	status, contentType, body := send(t, app, httptest.NewRequest(http.MethodGet, "/api/v1/broken", nil))

	assert.Equal(t, http.StatusInternalServerError, status, "status")
	assert.Equal(t, "application/json", contentType, "Content-Type")
	var response map[string]string
	require.NoError(t, json.Unmarshal([]byte(body), &response), "body %q", body)
	assert.Len(t, response, 1, "body %q", body)
	assert.True(t, strings.HasPrefix(response["error"], "error proxying request: "), "error %q", response["error"])
}

// newHttpApp registers ProcessRequest on the route patterns the service uses, proxying to a stand-in for MaaS.
func newHttpApp(t *testing.T, upstream *recordingUpstream) *fiber.App {
	t.Helper()
	upstreamServer := httptest.NewServer(upstream)
	t.Cleanup(upstreamServer.Close)
	handler := &ApiHttpHandler{
		BasicRequestCreator: func(method, url string) *httputils.HttpRequest {
			return httputils.Req(method, url, model.AuthCredentials{Username: "user", Password: "password"}.AuthHeaderProvider)
		},
		MaasAddr:  upstreamServer.URL,
		Namespace: testNamespace,
		TokenValidator: func(ctx context.Context, token string) (*jwt.Token, error) {
			return nil, nil
		},
	}
	app, err := fiberserver.New().Process()
	require.NoError(t, err)
	app.All("/api/*", handler.ProcessRequest)
	app.Get("/api-version", handler.ProcessRequest)
	return app
}

func send(t *testing.T, app *fiber.App, req *http.Request) (int, string, string) {
	t.Helper()
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err, "%s %s", req.Method, req.RequestURI)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read response body")
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(body)
}

type upstreamRequest struct {
	method          string
	requestURI      string
	body            string
	requestID       string
	originNamespace string
}

// recordingUpstream stands in for MaaS: it records the last request and answers 201 with a JSON body, or drops the
// connection without answering for /api/v1/broken.
type recordingUpstream struct {
	mu      sync.Mutex
	request upstreamRequest
}

func (u *recordingUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/broken" {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.request = upstreamRequest{
		method:          r.Method,
		requestURI:      r.RequestURI,
		body:            string(body),
		requestID:       r.Header.Get("X-Request-Id"),
		originNamespace: r.Header.Get("X-Origin-Namespace"),
	}
	u.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, `{"proxied":true}`)
}

func (u *recordingUpstream) reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.request = upstreamRequest{}
}

func (u *recordingUpstream) last() upstreamRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.request
}
