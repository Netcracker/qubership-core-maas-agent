package bg

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/fasthttp/websocket"

	"github.com/netcracker/qubership-core-lib-go/v3/security/rest"
)

func SecureWebSocketDial(ctx context.Context, sender *rest.M2MRequestSender, webSocketURL url.URL, dialer *websocket.Dialer, requestHeaders http.Header) (*websocket.Conn, *http.Response, error) {
	var conn *websocket.Conn
	var resp *http.Response
	var dialErr error
	err := sender.Send(ctx, webSocketURL.String(), func(token string) (int, error) {
		// the response of a rejected handshake (e.g. 401 for the Kubernetes token) stays open until it is replaced
		closeResponse(resp)
		conn, resp = nil, nil

		headers := requestHeaders.Clone()
		if headers == nil {
			headers = http.Header{}
		}
		if headers.Get("Host") == "" {
			headers.Set("Host", webSocketURL.Host)
		}
		headers.Set("Authorization", "Bearer "+token)

		conn, resp, dialErr = dialer.DialContext(ctx, webSocketURL.String(), headers)
		if resp != nil {
			return resp.StatusCode, dialErr
		}
		return 0, dialErr
	})
	if err != nil && !errors.Is(err, dialErr) {
		// the sender failed before the last dial, e.g. it could not read the legacy token after a 401, so
		// the response of the previous attempt is stale
		closeResponse(resp)
		conn, resp = nil, nil
	}
	return conn, resp, err
}

func closeResponse(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}
