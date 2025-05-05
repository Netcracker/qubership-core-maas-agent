package controller

import (
	"context"
	"net/url"

	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/httputils"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt"
	"github.com/netcracker/qubership-core-lib-go/v3/logging"

	"github.com/netcracker/qubership-core-lib-go/v3/serviceloader"
)

const (
	HTTP_X_ORIGIN_NAMESPACE    = "X-Origin-Namespace"
	HTTP_X_ORIGIN_MICROSERVICE = "X-Origin-Microservice"
	HTTP_X_COMPOSITE_ISOLATION = "X-Composite-Isolation"
)

type ApiHttpHandler struct {
	BasicRequestCreator        func(method, url string) *httputils.HttpRequest
	MaasAddr                   string
	Namespace                  string
	TokenValidator             func(ctx context.Context, token string) (*jwt.Token, error)
	CompositeIsolationDisabled bool
}

type DummySecurityWrapper struct {

}

type SecurityWrapperProvider interface {
	GetHandler(c *fiber.Ctx, handler func(context.Context) error, namespace string, validator func(ctx context.Context, token string) (*jwt.Token, error)) error
}

var (
	logger logging.Logger
)

func init() {
	logger = logging.GetLogger("controller")
	serviceloader.Register(1, &DummySecurityWrapper{})
}

func (v *ApiHttpHandler) SecurityWrapper(c *fiber.Ctx, handler func(context.Context) error) error {
	wrapper := serviceloader.MustLoad[SecurityWrapperProvider]()
	return wrapper.GetHandler(c, handler, v.Namespace, v.TokenValidator)
}

func (s *DummySecurityWrapper) GetHandler(c *fiber.Ctx, handler func(context.Context) error, namespace string, validator func(ctx context.Context, token string) (*jwt.Token, error)) error {
	ctx := c.UserContext()
	c.Request().Header.Set(HTTP_X_ORIGIN_NAMESPACE, namespace)
	logger.WarnC(ctx, "Use 'any_microservice' name as %v", HTTP_X_ORIGIN_MICROSERVICE)
c.Request().Header.Set(HTTP_X_ORIGIN_MICROSERVICE, "any_microservice")
	return handler(ctx)
}

func (v *ApiHttpHandler) ProcessRequest(fiberCtx *fiber.Ctx) error {
	return v.SecurityWrapper(fiberCtx, func(ctx context.Context) error {
		logger.DebugC(ctx, "Proxy request: %v", fiberCtx)

		requestUrl, err := url.Parse(v.MaasAddr)
		if err != nil {
			return respondWithError(ctx, fiberCtx, fiber.StatusInternalServerError, err.Error())
		}
		requestUrl.Path = fiberCtx.Path()
		requestUrl.RawQuery = fiberCtx.Context().QueryArgs().String()

		req := v.BasicRequestCreator(string(fiberCtx.Context().Method()), requestUrl.String()).
			SetRequestBodyBytes(fiberCtx.Request().Body())

		fiberCtx.Request().Header.VisitAll(func(key, value []byte) {
			req.AddHeader(string(key), string(value))
		})

		if v.CompositeIsolationDisabled {
			req.AddHeader(HTTP_X_COMPOSITE_ISOLATION, "disabled")
		}

		logger.InfoC(ctx, "Redirect request '%s' with namespace = '%s'",
			req.String(), string(fiberCtx.Request().Header.Peek(HTTP_X_ORIGIN_NAMESPACE)))

		code, body, err := req.Execute(ctx)
		if err != nil {
			return respondWithError(ctx, fiberCtx, fiber.StatusInternalServerError, "error proxying request: "+err.Error())
		}
		return respondWithBytes(fiberCtx, code, body)
	})
}

func respondWithError(ctx context.Context, c *fiber.Ctx, code int, msg string) error {
	return respondWithJson(ctx, c, code, map[string]string{"error": msg})
}

func RespondWithError(ctx context.Context, c *fiber.Ctx, code int, msg string) error {
	return respondWithJson(ctx, c, code, map[string]string{"error": msg})
}

func respondWithJson(ctx context.Context, c *fiber.Ctx, code int, payload interface{}) error {
	c.Response().Header.SetContentType("application/json")
	logger.DebugC(ctx, "Send response code: %v, body: %+v", code, payload)
	return c.Status(code).JSON(payload)
}

func respondWithBytes(ctx *fiber.Ctx, code int, response []byte) error {
	logger.DebugC(ctx.UserContext(), "Send response code: %v, body: %v", code, string(response))
	ctx.Response().Header.SetContentType("application/json")
	return ctx.Status(code).Send(response)
}

