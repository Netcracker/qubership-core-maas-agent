package httputils

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/model"

	"github.com/gofiber/fiber/v3"
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
