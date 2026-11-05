// Package m2mtest provides fake M2M token sources for tests of code that sends requests with the security library.
package m2mtest

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/netcracker/qubership-core-lib-go/v3/security"
	"github.com/netcracker/qubership-core-lib-go/v3/security/rest"
	"github.com/netcracker/qubership-core-lib-go/v3/security/tokensource"
	"github.com/netcracker/qubership-core-lib-go/v3/serviceloader"
)

const (
	// LegacyToken is returned by the fake security.TokenProvider.
	LegacyToken = "legacy-token"
	// K8sToken is returned by the fake Kubernetes token source.
	K8sToken = "k8s-token"
)

var (
	// LegacyCalls counts the legacy tokens requested since the last NewSender.
	LegacyCalls atomic.Int32
	// K8sCalls counts the Kubernetes tokens requested since the last NewSender.
	K8sCalls atomic.Int32

	legacyError atomic.Pointer[error]
	k8sError    atomic.Pointer[error]
)

type fakeTokenProvider struct{ security.DummyToken }

func (fakeTokenProvider) GetToken(ctx context.Context) (string, error) {
	LegacyCalls.Add(1)
	if err := legacyError.Load(); err != nil {
		return "", *err
	}
	return LegacyToken, nil
}

// fakeTokenSource replaces the Kubernetes projected token source of the security library.
type fakeTokenSource struct{}

func (fakeTokenSource) GetAudienceToken(ctx context.Context, audience tokensource.TokenAudience) (string, error) {
	K8sCalls.Add(1)
	if err := k8sError.Load(); err != nil {
		return "", *err
	}
	return K8sToken, nil
}

func (fakeTokenSource) GetServiceAccountToken(ctx context.Context) (string, error) {
	return "", errors.New("not supported")
}

func init() {
	serviceloader.Register(100, &fakeTokenProvider{})
	serviceloader.Register(100, &fakeTokenSource{})
}

// SetLegacyTokenError makes the fake legacy token provider fail until the test ends.
func SetLegacyTokenError(err error) { legacyError.Store(&err) }

// SetK8sTokenError makes the fake Kubernetes token source fail until the test ends.
func SetK8sTokenError(err error) { k8sError.Store(&err) }

// NewSender returns a sender for the given M2M_AUTH_MODE backed by the fake tokens. The fake state is reset when
// the test ends, so it can't leak into other tests.
func NewSender(t *testing.T, mode security.M2MAuthMode) *rest.M2MRequestSender {
	t.Helper()
	reset := func() {
		legacyError.Store(nil)
		k8sError.Store(nil)
		LegacyCalls.Store(0)
		K8sCalls.Store(0)
	}
	reset()
	t.Cleanup(reset)
	t.Setenv(security.M2MAuthModeEnv, string(mode))
	return rest.NewM2MRequestSender()
}
