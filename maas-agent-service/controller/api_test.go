package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	_ "net/http/httptest"
	"os"
	"testing"

	_ "github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/config"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/httputils"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/model"

	"github.com/gofiber/fiber/v2"
	jwt "github.com/golang-jwt/jwt/v5"
	_assert "github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

var (
	mockServer   *httptest.Server
	fiberRequest *fiber.Ctx
	username     string
	password     string
	apiHandler   *ApiHttpHandler
)

func setUp() {

	username = "username"
	password = "password"

	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		onTopicExistsValue := r.URL.Query().Get("onTopicExists")
		if onTopicExistsValue != "merge" {
			w.WriteHeader(http.StatusTeapot)
			w.Write([]byte("onTopicExists is empty. Check your changes"))
		}
		if !ok || user != username || pass != password {
			w.WriteHeader(http.StatusForbidden)
		}
		fmt.Fprintln(w, "test-server-response-OK")
	}))

	app := fiber.New()
	req := fasthttp.Request{}
	req.Header.SetMethod("POST")
	req.SetBody([]byte("abc"))
	req.URI().SetQueryString("onTopicExists=merge")

	fiberRequest = app.AcquireCtx(&fasthttp.RequestCtx{
		Request: req,
	})

	apiHandler = &ApiHttpHandler{
		BasicRequestCreator: func(method, url string) *httputils.HttpRequest {
			return httputils.Req(method, url, model.AuthCredentials{Username: username, Password: password}.AuthHeaderProvider)
		},
		MaasAddr:  mockServer.URL,
		Namespace: "maas-test",
		TokenValidator: func(ctx context.Context, token string) (*jwt.Token, error) {
			parser := jwt.Parser{}
			parsedToken, _, err := parser.ParseUnverified(token, jwt.MapClaims{})
			return parsedToken, err
		},
	}

}

func tearDown() {
	mockServer.Close()
}

func TestMain(m *testing.M) {
	setUp()
	exitCode := m.Run()
	tearDown()
	os.Exit(exitCode)
}

func TestApiHttpHandler_ProcessRequestOk(t *testing.T) {
	assert := _assert.New(t)

	if err := apiHandler.ProcessRequest(fiberRequest); err != nil {
		t.Errorf("ProcessRequest() error = %v", err)
	}

	response := fiberRequest.Response()
	assert.Equal(http.StatusOK, response.StatusCode())
}
