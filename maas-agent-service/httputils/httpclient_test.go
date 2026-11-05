package httputils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/internal/m2mtest"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/model"

	"github.com/gofiber/fiber/v2"
	"github.com/netcracker/qubership-core-lib-go/v3/security"
	"github.com/netcracker/qubership-core-lib-go/v3/security/rest"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestBuildRequest(t *testing.T) {
	ctx := context.Background()
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !(ok && user == "abc" && pass == "cde") {
			w.WriteHeader(http.StatusForbidden)
		}

		if r.Header.Get("custom") != "header" {
			w.WriteHeader(http.StatusBadRequest)
		}

		if r.Header.Get("X-Origin-Microservice") != "order-proc" {
			w.WriteHeader(http.StatusBadRequest)
		}

		fmt.Fprintln(w, "test-server-response-OK")
	}))
	defer mockServer.Close()

	body := "body"
	code, _, err := Req(
		fasthttp.MethodGet,
		mockServer.URL,
		model.AuthCredentials{Username: "abc", Password: "cde"}.AuthHeaderProvider,
	).
		SetHeader("custom", "header").
		AddHeader("X-Origin-Microservice", "order-proc").
		SetRequestBody(&body).
		Execute(ctx)

	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, code)
}

func TestBuildRequestSetBodyBytes(t *testing.T) {
	ctx := context.Background()
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)

		if string(buf) != "body" {
			w.WriteHeader(http.StatusBadRequest)
		}

		if r.Header.Get(HEADER_X_REQUEST_ID) != "1234" {
			w.WriteHeader(http.StatusBadRequest)
		}

		fmt.Fprintln(w, "test-server-response-OK")
	}))
	defer mockServer.Close()

	body := []byte("body")
	code, _, err := Req(fasthttp.MethodGet, mockServer.URL, nil).
		SetHeader("x-request-id", "1234").
		SetRequestBodyBytes(body).
		Execute(ctx)

	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, code)
}

func TestBuildRequestDefaultRequestId(t *testing.T) {
	ctx := context.Background()
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HEADER_X_REQUEST_ID) == "" {
			w.WriteHeader(http.StatusBadRequest)
		}

		fmt.Fprintln(w, "test-server-response-OK")
	}))
	defer mockServer.Close()

	body := []byte("body")
	code, _, err := Req(fasthttp.MethodGet, mockServer.URL, nil).
		SetRequestBodyBytes(body).
		Execute(ctx)

	assert.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, code)
}

type receivedRequest struct {
	auth string
	body string
}

// authServer answers every request with status(auth) and records what it received.
func authServer(t *testing.T, status func(auth string) int) (*httptest.Server, func() []receivedRequest) {
	var mu sync.Mutex
	var received []receivedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		auth := r.Header.Get("Authorization")
		mu.Lock()
		received = append(received, receivedRequest{auth: auth, body: string(body)})
		mu.Unlock()
		w.WriteHeader(status(auth))
	}))
	t.Cleanup(server.Close)
	return server, func() []receivedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]receivedRequest(nil), received...)
	}
}

func k8sOnly(auth string) int {
	if auth == "Bearer "+m2mtest.K8sToken {
		return http.StatusOK
	}
	return http.StatusUnauthorized
}

func TestM2MReqSendsLegacyToken(t *testing.T) {

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+m2mtest.LegacyToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer mockServer.Close()

	code, body, err := M2MReq(fasthttp.MethodGet, mockServer.URL, m2mtest.NewSender(t, security.M2MAuthModeLegacy)).Execute(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ok", string(body))
}

func TestM2MReqTokenError(t *testing.T) {
	sender := m2mtest.NewSender(t, security.M2MAuthModeLegacy)
	tokenErr := errors.New("no token")
	m2mtest.SetLegacyTokenError(tokenErr)

	var called atomic.Bool
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Store(true) }))
	defer mockServer.Close()

	code, body, err := M2MReq(fasthttp.MethodGet, mockServer.URL, sender).Execute(context.Background())
	assert.ErrorIs(t, err, tokenErr)
	assert.Equal(t, -1, code)
	assert.Nil(t, body)
	assert.False(t, called.Load())
}

func TestM2MReqNon2xxStatusAndHeaders(t *testing.T) {

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom") != "value" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer mockServer.Close()

	code, _, err := M2MReq(fasthttp.MethodPost, mockServer.URL, m2mtest.NewSender(t, security.M2MAuthModeLegacy)).
		SetHeader("X-Custom", "value").
		AddHeader(HEADER_X_REQUEST_ID, "id-1").
		SetRequestBody(new(string)).
		Execute(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, code)
}

func TestM2MReqConnectionRefused(t *testing.T) {
	mockServer := httptest.NewServer(http.NotFoundHandler())
	mockServer.Close()

	code, body, err := M2MReq(fasthttp.MethodGet, mockServer.URL, m2mtest.NewSender(t, security.M2MAuthModeLegacy)).Execute(context.Background())
	assert.Error(t, err)
	assert.Equal(t, -1, code)
	assert.Nil(t, body)
}

func TestReqAuthHeaderProviderError(t *testing.T) {
	providerErr := errors.New("boom")
	code, body, err := Req(fasthttp.MethodGet, "http://localhost:1", func(ctx context.Context) (string, error) {
		return "", providerErr
	}).Execute(context.Background())
	assert.ErrorIs(t, err, providerErr)
	assert.Equal(t, -1, code)
	assert.Nil(t, body)
}

func TestReqConnectionRefused(t *testing.T) {
	mockServer := httptest.NewServer(http.NotFoundHandler())
	mockServer.Close()

	code, body, err := Req(fasthttp.MethodGet, mockServer.URL, nil).Execute(context.Background())
	assert.Error(t, err)
	assert.Equal(t, -1, code)
	assert.Nil(t, body)
}

func execPost(sender *rest.M2MRequestSender, url string) (int, error) {
	body := "payload"
	code, _, err := M2MReq(fasthttp.MethodPost, url, sender).SetRequestBody(&body).Execute(context.Background())
	return code, err
}

func TestM2MReqK8sModeSendsOnlyK8sToken(t *testing.T) {
	sender := m2mtest.NewSender(t, security.M2MAuthModeK8s)
	server, received := authServer(t, k8sOnly)

	code, err := execPost(sender, server.URL)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []receivedRequest{{auth: "Bearer " + m2mtest.K8sToken, body: "payload"}}, received())
	assert.Zero(t, m2mtest.LegacyCalls.Load(), "k8s mode must not read the legacy token")
}

func TestM2MReqK8sModeDoesNotFallBackOn401(t *testing.T) {
	sender := m2mtest.NewSender(t, security.M2MAuthModeK8s)
	server, received := authServer(t, func(string) int { return http.StatusUnauthorized })

	code, err := execPost(sender, server.URL)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Len(t, received(), 1)
	assert.Zero(t, m2mtest.LegacyCalls.Load())
}

func TestM2MReqK8sModeTokenError(t *testing.T) {
	sender := m2mtest.NewSender(t, security.M2MAuthModeK8s)
	tokenErr := errors.New("no k8s token")
	m2mtest.SetK8sTokenError(tokenErr)
	server, received := authServer(t, k8sOnly)

	code, err := execPost(sender, server.URL)
	assert.ErrorIs(t, err, tokenErr)
	assert.Equal(t, -1, code)
	assert.Empty(t, received())
	assert.Zero(t, m2mtest.LegacyCalls.Load(), "k8s mode must not fall back to the legacy token")
}

func TestM2MReqHybridModeSendsK8sToken(t *testing.T) {
	sender := m2mtest.NewSender(t, security.M2MAuthModeHybrid)
	server, received := authServer(t, k8sOnly)

	code, err := execPost(sender, server.URL)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []receivedRequest{{auth: "Bearer " + m2mtest.K8sToken, body: "payload"}}, received())
	assert.Zero(t, m2mtest.LegacyCalls.Load())
}

func TestM2MReqHybridModeResendsWithLegacyTokenOn401(t *testing.T) {
	sender := m2mtest.NewSender(t, security.M2MAuthModeHybrid)
	// the receiver only knows the legacy token
	server, received := authServer(t, func(auth string) int {
		if auth == "Bearer "+m2mtest.LegacyToken {
			return http.StatusOK
		}
		return http.StatusUnauthorized
	})

	code, err := execPost(sender, server.URL)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	// the request body must be sent again on the second attempt
	assert.Equal(t, []receivedRequest{
		{auth: "Bearer " + m2mtest.K8sToken, body: "payload"},
		{auth: "Bearer " + m2mtest.LegacyToken, body: "payload"},
	}, received())

	// the sender remembers that the target only accepts the legacy token
	code, err = execPost(sender, server.URL)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, received(), 3)
	assert.Equal(t, "Bearer "+m2mtest.LegacyToken, received()[2].auth)
}

func TestM2MReqHybridModeFallsBackWhenK8sTokenUnavailable(t *testing.T) {
	sender := m2mtest.NewSender(t, security.M2MAuthModeHybrid)
	tokenErr := errors.New("no k8s token")
	m2mtest.SetK8sTokenError(tokenErr)
	server, received := authServer(t, func(string) int { return http.StatusOK })

	code, err := execPost(sender, server.URL)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []receivedRequest{{auth: "Bearer " + m2mtest.LegacyToken, body: "payload"}}, received())
}

func TestM2MReqHybridModeBothTokensRejected(t *testing.T) {
	sender := m2mtest.NewSender(t, security.M2MAuthModeHybrid)
	server, received := authServer(t, func(string) int { return http.StatusUnauthorized })

	code, err := execPost(sender, server.URL)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Len(t, received(), 2)
}
