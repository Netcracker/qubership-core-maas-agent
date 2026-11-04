package httputils

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/netcracker/qubership-core-lib-go/v3/context-propagation/ctxhelper"
	"github.com/netcracker/qubership-core-lib-go/v3/logging"
	"github.com/netcracker/qubership-core-lib-go/v3/security/rest"
	tlsUtils "github.com/netcracker/qubership-core-lib-go/v3/utils"
	"github.com/valyala/fasthttp"
)

const HEADER_X_REQUEST_ID = "X-Request-Id"

var (
	fasthttpClient = &fasthttp.Client{
		TLSConfig:                     tlsUtils.GetTlsConfig(),
		MaxIdleConnDuration:           30 * time.Second,
		DisableHeaderNamesNormalizing: true,
		DisablePathNormalizing:        true,
		DialDualStack:                 true,
		WriteTimeout:                  30 * time.Second,
		ReadTimeout:                   130 * time.Second, // should be greater than watch requests timeout 120sec
	}
	log logging.Logger
)

func init() {
	log = logging.GetLogger("utils/http-client")
}

type HttpRequest struct {
	method             string
	url                string
	headers            http.Header
	requestBody        *string
	requestBodyBytes   []byte
	authHeaderProvider func(ctx context.Context) (string, error)
	m2mSender          *rest.M2MRequestSender
}

func (c HttpRequest) String() string {
	return fmt.Sprintf("%s %s", c.method, c.url)
}

func Req(method string, url string, authHeaderProvider func(ctx context.Context) (string, error)) *HttpRequest {
	return &HttpRequest{method: method, url: url, headers: http.Header{}, authHeaderProvider: authHeaderProvider}
}

func M2MReq(method string, url string, sender *rest.M2MRequestSender) *HttpRequest {
	return &HttpRequest{method: method, url: url, headers: http.Header{}, m2mSender: sender}
}

func (c *HttpRequest) SetRequestBody(body *string) *HttpRequest {
	c.requestBody = body
	return c
}

func (c *HttpRequest) SetRequestBodyBytes(body []byte) *HttpRequest {
	c.requestBodyBytes = body
	return c
}

func (c *HttpRequest) SetHeader(name string, value string) *HttpRequest {
	c.headers.Set(name, value)
	return c
}

func (c *HttpRequest) AddHeader(name string, value string) *HttpRequest {
	c.headers.Add(name, value)
	return c
}

func (c *HttpRequest) Execute(ctx context.Context) (code int, body []byte, err error) {
	if err := ctxhelper.AddSerializableContextData(ctx, c.headers.Add); err != nil {
		return -1, nil, fmt.Errorf("cannot add context data: %w", err)
	}

	if c.headers.Get(HEADER_X_REQUEST_ID) == "" {
		requestId := uuid.New().String()
		log.WarnC(ctx, "No request id is set. Create new one: %s", requestId)
		c.headers.Add(HEADER_X_REQUEST_ID, requestId)
	}

	if c.m2mSender != nil {
		err = c.m2mSender.Send(ctx, c.url, func(token string) (int, error) {
			var sendErr error
			code, body, sendErr = c.do(ctx, "Bearer "+token)
			if sendErr != nil {
				return 0, sendErr
			}
			return code, nil
		})
		if err != nil {
			return -1, nil, err
		}
		return code, body, nil
	}

	authHeader := ""
	if c.authHeaderProvider != nil {
		authHeader, err = c.authHeaderProvider(ctx)
		if err != nil {
			return -1, nil, fmt.Errorf("error getting auth header: %w", err)
		}
	}
	return c.do(ctx, authHeader)
}

func (c *HttpRequest) do(ctx context.Context, authHeader string) (int, []byte, error) {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	req.Header.SetMethod(c.method)
	req.SetRequestURI(c.url)

	if c.requestBody != nil {
		req.SetBodyString(*c.requestBody)
	}
	if len(c.requestBodyBytes) > 0 {
		req.SetBody(c.requestBodyBytes)
	}

	for name := range c.headers {
		for _, value := range c.headers.Values(name) {
			req.Header.Add(name, value)
		}
	}

	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	log.InfoC(ctx, "Execute request: %+v", c)
	err := fasthttpClient.Do(req, resp)

	if err != nil {
		log.ErrorC(ctx, "Error execute http request: %v", err)
		return -1, nil, err
	}

	body := make([]byte, len(resp.Body()))
	copy(body, resp.Body())

	return resp.StatusCode(), body, nil
}
