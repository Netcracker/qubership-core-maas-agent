package bg

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/netcracker/qubership-core-lib-go/v3/security"
	"github.com/netcracker/qubership-core-lib-go/v3/security/rest"
	"github.com/netcracker/qubership-core-lib-go/v3/security/tokensource"
	"github.com/netcracker/qubership-core-lib-go/v3/serviceloader"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/maasservice"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var tokenError atomic.Pointer[error]

type fakeTokenProvider struct{ security.DummyToken }

var legacyCalls atomic.Int32

func (fakeTokenProvider) GetToken(ctx context.Context) (string, error) {
	legacyCalls.Add(1)
	if err := tokenError.Load(); err != nil {
		return "", *err
	}
	return "legacy-token", nil
}

// fakeTokenSource replaces the Kubernetes projected token source of the security library.
type fakeTokenSource struct{}

var k8sTokenError atomic.Pointer[error]

func (fakeTokenSource) GetAudienceToken(ctx context.Context, audience tokensource.TokenAudience) (string, error) {
	if err := k8sTokenError.Load(); err != nil {
		return "", *err
	}
	return "k8s-token", nil
}

func (fakeTokenSource) GetServiceAccountToken(ctx context.Context) (string, error) {
	return "", errors.New("not supported")
}

func init() {
	serviceloader.Register(100, &fakeTokenProvider{})
	serviceloader.Register(100, &fakeTokenSource{})
}

// newSender returns a sender for the given M2M_AUTH_MODE with fresh fake tokens.
func newSender(t *testing.T, mode security.M2MAuthMode) *rest.M2MRequestSender {
	t.Setenv(security.M2MAuthModeEnv, string(mode))
	tokenError.Store(nil)
	k8sTokenError.Store(nil)
	legacyCalls.Store(0)
	return rest.NewM2MRequestSender()
}

// authRejectingServer rejects the handshake with 401 unless accept(auth) is true, and records every Authorization.
func authRejectingServer(t *testing.T, accept func(auth string) bool) (url.URL, func() []string) {
	var mu sync.Mutex
	var auths []string
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		auths = append(auths, auth)
		mu.Unlock()
		if !accept(auth) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if conn, err := upgrader.Upgrade(w, r, nil); err == nil {
			conn.Close()
		}
	}))
	t.Cleanup(server.Close)
	u, err := url.Parse("ws" + strings.TrimPrefix(server.URL, "http"))
	require.NoError(t, err)
	return *u, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), auths...)
	}
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
	conn, resp, err := SecureWebSocketDial(context.Background(), newSender(t, security.M2MAuthModeLegacy), u, &websocket.Dialer{}, headers)
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

	conn, _, err := SecureWebSocketDial(context.Background(), newSender(t, security.M2MAuthModeLegacy), u, &websocket.Dialer{}, nil)
	require.NoError(t, err)
	conn.Close()
}

func TestSecureWebSocketDialHandshakeRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	u, _ := url.Parse("ws" + strings.TrimPrefix(server.URL, "http"))

	conn, resp, err := SecureWebSocketDial(context.Background(), newSender(t, security.M2MAuthModeLegacy), *u, &websocket.Dialer{}, nil)
	assert.Error(t, err)
	assert.Nil(t, conn)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestSecureWebSocketDialConnectionRefused(t *testing.T) {
	server, u := wsServer(t, func(r *http.Request, conn *websocket.Conn) {})
	server.Close()

	conn, resp, err := SecureWebSocketDial(context.Background(), newSender(t, security.M2MAuthModeLegacy), u, &websocket.Dialer{}, nil)
	assert.Error(t, err)
	assert.Nil(t, conn)
	assert.Nil(t, resp)
}

func TestSecureWebSocketDialTokenError(t *testing.T) {
	var dialed atomic.Bool
	_, u := wsServer(t, func(r *http.Request, conn *websocket.Conn) { dialed.Store(true) })

	sender := newSender(t, security.M2MAuthModeLegacy)
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
	service := maasservice.MaaSService{CpAddr: cpAddr, M2MRequestSender: newSender(t, security.M2MAuthModeLegacy)}
	go subscribeToControlPlaneForVersionChanges(ctx, service, ch)

	select {
	case versions := <-ch:
		assert.Equal(t, model.CpVersionsDto{{Version: "v1", Stage: "ACTIVE"}}, versions)
		assert.Equal(t, "Bearer legacy-token", gotAuth.Load())
	case <-time.After(5 * time.Second):
		t.Fatal("no versions received from the control-plane watcher")
	}
}

func TestSecureWebSocketDialK8sModeSendsOnlyK8sToken(t *testing.T) {
	u, auths := authRejectingServer(t, func(auth string) bool { return auth == "Bearer k8s-token" })

	conn, _, err := SecureWebSocketDial(context.Background(), newSender(t, security.M2MAuthModeK8s), u, &websocket.Dialer{}, nil)
	require.NoError(t, err)
	conn.Close()
	assert.Equal(t, []string{"Bearer k8s-token"}, auths())
	assert.Zero(t, legacyCalls.Load(), "k8s mode must not read the legacy token")
}

func TestSecureWebSocketDialK8sModeDoesNotFallBackOn401(t *testing.T) {
	u, auths := authRejectingServer(t, func(string) bool { return false })

	conn, resp, err := SecureWebSocketDial(context.Background(), newSender(t, security.M2MAuthModeK8s), u, &websocket.Dialer{}, nil)
	assert.Error(t, err)
	assert.Nil(t, conn)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, []string{"Bearer k8s-token"}, auths())
	assert.Zero(t, legacyCalls.Load())
}

func TestSecureWebSocketDialK8sModeTokenError(t *testing.T) {
	u, auths := authRejectingServer(t, func(string) bool { return true })
	sender := newSender(t, security.M2MAuthModeK8s)
	tokenErr := errors.New("no k8s token")
	k8sTokenError.Store(&tokenErr)

	conn, _, err := SecureWebSocketDial(context.Background(), sender, u, &websocket.Dialer{}, nil)
	assert.ErrorIs(t, err, tokenErr)
	assert.Nil(t, conn)
	assert.Empty(t, auths())
	assert.Zero(t, legacyCalls.Load())
}

func TestSecureWebSocketDialHybridModeSendsK8sToken(t *testing.T) {
	u, auths := authRejectingServer(t, func(auth string) bool { return auth == "Bearer k8s-token" })

	conn, _, err := SecureWebSocketDial(context.Background(), newSender(t, security.M2MAuthModeHybrid), u, &websocket.Dialer{}, nil)
	require.NoError(t, err)
	conn.Close()
	assert.Equal(t, []string{"Bearer k8s-token"}, auths())
	assert.Zero(t, legacyCalls.Load())
}

func TestSecureWebSocketDialHybridModeRedialsWithLegacyTokenOn401(t *testing.T) {
	// the receiver only knows the legacy token
	u, auths := authRejectingServer(t, func(auth string) bool { return auth == "Bearer legacy-token" })

	conn, resp, err := SecureWebSocketDial(context.Background(), newSender(t, security.M2MAuthModeHybrid), u, &websocket.Dialer{}, nil)
	require.NoError(t, err)
	conn.Close()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	assert.Equal(t, []string{"Bearer k8s-token", "Bearer legacy-token"}, auths())
}

func TestSecureWebSocketDialHybridModeFallsBackWhenK8sTokenUnavailable(t *testing.T) {
	u, auths := authRejectingServer(t, func(auth string) bool { return auth == "Bearer legacy-token" })
	sender := newSender(t, security.M2MAuthModeHybrid)
	tokenErr := errors.New("no k8s token")
	k8sTokenError.Store(&tokenErr)

	conn, _, err := SecureWebSocketDial(context.Background(), sender, u, &websocket.Dialer{}, nil)
	require.NoError(t, err)
	conn.Close()
	assert.Equal(t, []string{"Bearer legacy-token"}, auths())
}
