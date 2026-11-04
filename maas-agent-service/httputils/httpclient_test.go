package httputils

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/model"

	"github.com/gofiber/fiber/v2"
	"github.com/netcracker/qubership-core-lib-go/v3/security"
	"github.com/netcracker/qubership-core-lib-go/v3/security/rest"
	"github.com/netcracker/qubership-core-lib-go/v3/serviceloader"
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

type fakeTokenProvider struct{ security.DummyToken }

var tokenError atomic.Pointer[error]

func (fakeTokenProvider) GetToken(ctx context.Context) (string, error) {
	if err := tokenError.Load(); err != nil {
		return "", *err
	}
	return "legacy-token", nil
}

func TestM2MReqSendsLegacyToken(t *testing.T) {
	t.Setenv(security.M2MAuthModeEnv, string(security.M2MAuthModeLegacy))
	serviceloader.Register(100, &fakeTokenProvider{})

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer legacy-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer mockServer.Close()

	code, body, err := M2MReq(fasthttp.MethodGet, mockServer.URL, rest.NewM2MRequestSender()).Execute(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ok", string(body))
}

func TestM2MReqTokenError(t *testing.T) {
	t.Setenv(security.M2MAuthModeEnv, string(security.M2MAuthModeLegacy))
	serviceloader.Register(100, &fakeTokenProvider{})
	sender := rest.NewM2MRequestSender()
	tokenErr := errors.New("no token")
	tokenError.Store(&tokenErr)
	defer tokenError.Store(nil)

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
	t.Setenv(security.M2MAuthModeEnv, string(security.M2MAuthModeLegacy))
	serviceloader.Register(100, &fakeTokenProvider{})

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom") != "value" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer mockServer.Close()

	code, _, err := M2MReq(fasthttp.MethodPost, mockServer.URL, rest.NewM2MRequestSender()).
		SetHeader("X-Custom", "value").
		AddHeader(HEADER_X_REQUEST_ID, "id-1").
		SetRequestBody(new(string)).
		Execute(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, code)
}

func TestM2MReqConnectionRefused(t *testing.T) {
	t.Setenv(security.M2MAuthModeEnv, string(security.M2MAuthModeLegacy))
	serviceloader.Register(100, &fakeTokenProvider{})
	mockServer := httptest.NewServer(http.NotFoundHandler())
	mockServer.Close()

	code, body, err := M2MReq(fasthttp.MethodGet, mockServer.URL, rest.NewM2MRequestSender()).Execute(context.Background())
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
