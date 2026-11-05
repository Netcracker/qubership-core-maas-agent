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
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/internal/m2mtest"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/maasservice"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	conn, resp, err := SecureWebSocketDial(context.Background(), m2mtest.NewSender(t, security.M2MAuthModeLegacy), u, &websocket.Dialer{}, headers)
	require.NoError(t, err)
	defer conn.Close()

	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	_, _, _ = conn.ReadMessage() // wait for server handler to finish
	assert.Equal(t, "Bearer "+m2mtest.LegacyToken, gotAuth.Load())
	assert.Equal(t, "value", gotCustom.Load())
	// the caller's headers must not be mutated
	assert.Equal(t, []string{"Bearer caller-token"}, headers["Authorization"])
	assert.Empty(t, headers.Get("Host"))
}

func TestSecureWebSocketDialNilHeaders(t *testing.T) {
	_, u := wsServer(t, func(r *http.Request, conn *websocket.Conn) {})

	conn, _, err := SecureWebSocketDial(context.Background(), m2mtest.NewSender(t, security.M2MAuthModeLegacy), u, &websocket.Dialer{}, nil)
	require.NoError(t, err)
	conn.Close()
}

func TestSecureWebSocketDialHandshakeRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	u, _ := url.Parse("ws" + strings.TrimPrefix(server.URL, "http"))

	conn, resp, err := SecureWebSocketDial(context.Background(), m2mtest.NewSender(t, security.M2MAuthModeLegacy), *u, &websocket.Dialer{}, nil)
	assert.Error(t, err)
	assert.Nil(t, conn)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestSecureWebSocketDialConnectionRefused(t *testing.T) {
	server, u := wsServer(t, func(r *http.Request, conn *websocket.Conn) {})
	server.Close()

	conn, resp, err := SecureWebSocketDial(context.Background(), m2mtest.NewSender(t, security.M2MAuthModeLegacy), u, &websocket.Dialer{}, nil)
	assert.Error(t, err)
	assert.Nil(t, conn)
	assert.Nil(t, resp)
}

func TestSecureWebSocketDialTokenError(t *testing.T) {
	var dialed atomic.Bool
	_, u := wsServer(t, func(r *http.Request, conn *websocket.Conn) { dialed.Store(true) })

	sender := m2mtest.NewSender(t, security.M2MAuthModeLegacy)
	tokenErr := errors.New("no token")
	m2mtest.SetLegacyTokenError(tokenErr)

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
	service := maasservice.MaaSService{CpAddr: cpAddr}
	go subscribeToControlPlaneForVersionChanges(ctx, service, m2mtest.NewSender(t, security.M2MAuthModeLegacy), ch)

	select {
	case versions := <-ch:
		assert.Equal(t, model.CpVersionsDto{{Version: "v1", Stage: "ACTIVE"}}, versions)
		assert.Equal(t, "Bearer "+m2mtest.LegacyToken, gotAuth.Load())
	case <-time.After(5 * time.Second):
		t.Fatal("no versions received from the control-plane watcher")
	}
}

func TestSecureWebSocketDialK8sModeSendsOnlyK8sToken(t *testing.T) {
	u, auths := authRejectingServer(t, func(auth string) bool { return auth == "Bearer "+m2mtest.K8sToken })

	conn, _, err := SecureWebSocketDial(context.Background(), m2mtest.NewSender(t, security.M2MAuthModeK8s), u, &websocket.Dialer{}, nil)
	require.NoError(t, err)
	conn.Close()
	assert.Equal(t, []string{"Bearer " + m2mtest.K8sToken}, auths())
	assert.Zero(t, m2mtest.LegacyCalls.Load(), "k8s mode must not read the legacy token")
}

func TestSecureWebSocketDialK8sModeDoesNotFallBackOn401(t *testing.T) {
	u, auths := authRejectingServer(t, func(string) bool { return false })

	conn, resp, err := SecureWebSocketDial(context.Background(), m2mtest.NewSender(t, security.M2MAuthModeK8s), u, &websocket.Dialer{}, nil)
	assert.Error(t, err)
	assert.Nil(t, conn)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, []string{"Bearer " + m2mtest.K8sToken}, auths())
	assert.Zero(t, m2mtest.LegacyCalls.Load())
}

func TestSecureWebSocketDialK8sModeTokenError(t *testing.T) {
	u, auths := authRejectingServer(t, func(string) bool { return true })
	sender := m2mtest.NewSender(t, security.M2MAuthModeK8s)
	tokenErr := errors.New("no k8s token")
	m2mtest.SetK8sTokenError(tokenErr)

	conn, _, err := SecureWebSocketDial(context.Background(), sender, u, &websocket.Dialer{}, nil)
	assert.ErrorIs(t, err, tokenErr)
	assert.Nil(t, conn)
	assert.Empty(t, auths())
	assert.Zero(t, m2mtest.LegacyCalls.Load())
}

func TestSecureWebSocketDialHybridModeSendsK8sToken(t *testing.T) {
	u, auths := authRejectingServer(t, func(auth string) bool { return auth == "Bearer "+m2mtest.K8sToken })

	conn, _, err := SecureWebSocketDial(context.Background(), m2mtest.NewSender(t, security.M2MAuthModeHybrid), u, &websocket.Dialer{}, nil)
	require.NoError(t, err)
	conn.Close()
	assert.Equal(t, []string{"Bearer " + m2mtest.K8sToken}, auths())
	assert.Zero(t, m2mtest.LegacyCalls.Load())
}

func TestSecureWebSocketDialHybridModeRedialsWithLegacyTokenOn401(t *testing.T) {
	// the receiver only knows the legacy token
	u, auths := authRejectingServer(t, func(auth string) bool { return auth == "Bearer "+m2mtest.LegacyToken })

	conn, resp, err := SecureWebSocketDial(context.Background(), m2mtest.NewSender(t, security.M2MAuthModeHybrid), u, &websocket.Dialer{}, nil)
	require.NoError(t, err)
	conn.Close()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	assert.Equal(t, []string{"Bearer " + m2mtest.K8sToken, "Bearer " + m2mtest.LegacyToken}, auths())
}

func TestSecureWebSocketDialHybridModeFallsBackWhenK8sTokenUnavailable(t *testing.T) {
	u, auths := authRejectingServer(t, func(auth string) bool { return auth == "Bearer "+m2mtest.LegacyToken })
	sender := m2mtest.NewSender(t, security.M2MAuthModeHybrid)
	tokenErr := errors.New("no k8s token")
	m2mtest.SetK8sTokenError(tokenErr)

	conn, _, err := SecureWebSocketDial(context.Background(), sender, u, &websocket.Dialer{}, nil)
	require.NoError(t, err)
	conn.Close()
	assert.Equal(t, []string{"Bearer " + m2mtest.LegacyToken}, auths())
}

func TestSecureWebSocketDialHybridModeLegacyTokenErrorAfter401(t *testing.T) {
	u, auths := authRejectingServer(t, func(string) bool { return false })
	sender := m2mtest.NewSender(t, security.M2MAuthModeHybrid)
	tokenErr := errors.New("no legacy token")
	m2mtest.SetLegacyTokenError(tokenErr)

	conn, resp, err := SecureWebSocketDial(context.Background(), sender, u, &websocket.Dialer{}, nil)
	assert.ErrorIs(t, err, tokenErr)
	assert.Nil(t, conn)
	assert.Nil(t, resp, "the response of the first attempt must not be returned for the failed second attempt")
	assert.Equal(t, []string{"Bearer " + m2mtest.K8sToken}, auths())
}
