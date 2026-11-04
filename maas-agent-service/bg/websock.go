package bg

import (
	"context"
	"net/http"
	"net/url"

	"github.com/fasthttp/websocket"

	"github.com/netcracker/qubership-core-lib-go/v3/security/rest"
)

func SecureWebSocketDial(ctx context.Context, sender *rest.M2MRequestSender, webSocketURL url.URL, dialer *websocket.Dialer, requestHeaders http.Header) (*websocket.Conn, *http.Response, error) {
	var conn *websocket.Conn
	var resp *http.Response
	err := sender.Send(ctx, webSocketURL.String(), func(token string) (int, error) {
		headers := requestHeaders.Clone()
		if headers == nil {
			headers = http.Header{}
		}
		if headers.Get("Host") == "" {
			headers.Set("Host", webSocketURL.Host)
		}
		headers.Set("Authorization", "Bearer "+token)

		var dialErr error
		conn, resp, dialErr = dialer.DialContext(ctx, webSocketURL.String(), headers)
		if resp != nil {
			return resp.StatusCode, dialErr
		}
		return 0, dialErr
	})
	return conn, resp, err
}
