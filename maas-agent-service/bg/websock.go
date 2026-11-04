package bg

import (
	"context"
	"net/http"
	"net/url"

	"github.com/fasthttp/websocket"

	"github.com/netcracker/qubership-core-lib-go/v3/security/rest"
)

// SecureWebSocketDial dials webSocketURL with the M2M token chosen by sender according to M2M_AUTH_MODE.
// In hybrid mode the handshake is retried with the legacy M2M token when the receiver answers 401.
// Any Authorization value in requestHeaders is replaced with the M2M token.
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
