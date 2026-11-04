package bg

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/netcracker/qubership-core-lib-go/v3/security"
	"github.com/netcracker/qubership-core-lib-go/v3/security/rest"
	"github.com/netcracker/qubership-core-lib-go/v3/serviceloader"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/maasservice"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var tokenError atomic.Pointer[error]

type fakeTokenProvider struct{ security.DummyToken }

func (fakeTokenProvider) GetToken(ctx context.Context) (string, error) {
	if err := tokenError.Load(); err != nil {
		return "", *err
	}
	return "legacy-token", nil
}

func init() {
	serviceloader.Register(100, &fakeTokenProvider{})
}

func newSender(t *testing.T) *rest.M2MRequestSender {
	t.Setenv(security.M2MAuthModeEnv, string(security.M2MAuthModeLegacy))
	tokenError.Store(nil)
	return rest.NewM2MRequestSender()
}

func wsServer(t *testing.T, handler func(r *http.Request, conn *websocket.Conn)) (*httptest.Server, url.URL) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handler(r, conn)
	}))
	t.Cleanup(server.Close)
	u, err := url.Parse("ws" + strings.TrimPrefix(server.URL, "http"))
	require.NoError(t, err)
	return server, *u
}

func TestSecureWebSocketDialSendsM2MToken(t *testing.T) {
	var gotAuth, gotCustom atomic.Value
	_, u := wsServer(t, func(r *http.Request, conn *websocket.Conn) {
		gotAuth.Store(r.Header.Get("Authorization"))
		gotCustom.Store(r.Header.Get("X-Custom"))
	})

	headers := http.Header{"X-Custom": {"value"}, "Authorization": {"Bearer caller-token"}}
	conn, resp, err := SecureWebSocketDial(context.Background(), newSender(t), u, &websocket.Dialer{}, headers)
	require.NoError(t, err)
	defer conn.Close()

	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	_, _, _ = conn.ReadMessage() // wait for server handler to finish
	assert.Equal(t, "Bearer legacy-token", gotAuth.Load())
	assert.Equal(t, "value", gotCustom.Load())
	// the caller's headers must not be mutated
	assert.Equal(t, []string{"Bearer caller-token"}, headers["Authorization"])
	assert.Empty(t, headers.Get("Host"))
}

func TestSecureWebSocketDialNilHeaders(t *testing.T) {
	_, u := wsServer(t, func(r *http.Request, conn *websocket.Conn) {})

	conn, _, err := SecureWebSocketDial(context.Background(), newSender(t), u, &websocket.Dialer{}, nil)
	require.NoError(t, err)
	conn.Close()
}

func TestSecureWebSocketDialHandshakeRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	u, _ := url.Parse("ws" + strings.TrimPrefix(server.URL, "http"))

	conn, resp, err := SecureWebSocketDial(context.Background(), newSender(t), *u, &websocket.Dialer{}, nil)
	assert.Error(t, err)
	assert.Nil(t, conn)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestSecureWebSocketDialConnectionRefused(t *testing.T) {
	server, u := wsServer(t, func(r *http.Request, conn *websocket.Conn) {})
	server.Close()

	conn, resp, err := SecureWebSocketDial(context.Background(), newSender(t), u, &websocket.Dialer{}, nil)
	assert.Error(t, err)
	assert.Nil(t, conn)
	assert.Nil(t, resp)
}

func TestSecureWebSocketDialTokenError(t *testing.T) {
	var dialed atomic.Bool
	_, u := wsServer(t, func(r *http.Request, conn *websocket.Conn) { dialed.Store(true) })

	sender := newSender(t)
	tokenErr := errors.New("no token")
	tokenError.Store(&tokenErr)

	conn, _, err := SecureWebSocketDial(context.Background(), sender, u, &websocket.Dialer{}, nil)
	assert.ErrorIs(t, err, tokenErr)
	assert.Nil(t, conn)
	assert.False(t, dialed.Load())
}

func TestSubscribeToControlPlaneForVersionChanges(t *testing.T) {
	var gotAuth atomic.Value
	server, _ := wsServer(t, func(r *http.Request, conn *websocket.Conn) {
		gotAuth.Store(r.Header.Get("Authorization"))
		_ = conn.WriteJSON(model.CpWatcherMessageDto{State: model.CpVersionsDto{{Version: "v1", Stage: "ACTIVE"}}})
		time.Sleep(time.Second) // keep the connection open until the reader got the message
	})
	cpAddr, err := url.Parse(server.URL)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan model.CpVersionsDto, 1)
	service := maasservice.MaaSService{CpAddr: cpAddr, M2MRequestSender: newSender(t)}
	go subscribeToControlPlaneForVersionChanges(ctx, service, ch)

	select {
	case versions := <-ch:
		assert.Equal(t, model.CpVersionsDto{{Version: "v1", Stage: "ACTIVE"}}, versions)
		assert.Equal(t, "Bearer legacy-token", gotAuth.Load())
	case <-time.After(5 * time.Second):
		t.Fatal("no versions received from the control-plane watcher")
	}
}
