package bg

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/maasservice"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/model"

	"github.com/fasthttp/websocket"
	"github.com/netcracker/qubership-core-lib-go/v3/logging"
	"github.com/netcracker/qubership-core-lib-go/v3/security/rest"
	tlsUtils "github.com/netcracker/qubership-core-lib-go/v3/utils"
)

const ApiCpWatcherPath = "api/v2/control-plane/versions/watch"

var logger = logging.GetLogger("bg-sync")

// SubscribeToControlPlaneWatcher
//
//	maas-agent subscribes to control-plane blue-green versions web-socket and sending changes to maas
func SubscribeToControlPlaneWatcher(ctx context.Context, httpService maasservice.MaaSService, m2mSender *rest.M2MRequestSender) {
	ch := make(chan model.CpVersionsDto)
	go subscribeToControlPlaneForVersionChanges(ctx, httpService, m2mSender, ch)
	go notifyMaasAboutVersionChanges(ctx, httpService, ch)
}

func subscribeToControlPlaneForVersionChanges(ctx context.Context, httpService maasservice.MaaSService, m2mSender *rest.M2MRequestSender, ch chan<- model.CpVersionsDto) {
	dialer := &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 45 * time.Second,
		TLSClientConfig:  tlsUtils.GetTlsConfig(),
	}
	protocol := "ws"
	if tlsUtils.IsTlsEnabled() {
		protocol = "wss"
	}

	url := *httpService.CpAddr // copy struct
	url.Scheme = protocol
	url.Path = ApiCpWatcherPath

	defer logger.InfoC(ctx, "B/G version change listener exited")

	var versions model.CpVersionsDto = nil
	for {
		conn, resp, err := SecureWebSocketDial(ctx, m2mSender, url, dialer, nil)
		if err != nil {
			if conn != nil {
				conn.Close()
			}
			logger.ErrorC(ctx, "Error connecting to control-plane watcher: %v, Retrying...", err)
			logger.WarnC(ctx, "Sleeping for 5 sec")
			time.Sleep(5 * time.Second)
			continue
		}

		logger.InfoC(ctx, "Control-plane watcher response: %v", resp)
		for {
			var change model.CpWatcherMessageDto
			err := conn.ReadJSON(&change)
			if err != nil {
				if IsContextCancelled(ctx) {
					return
				}

				logger.ErrorC(ctx, "Error reading versions from control-plane: %s", err)
				logger.InfoC(ctx, "Websocket to control-plane will be reconnected.")
				CancelableSleep(ctx, 5*time.Second)
				break
			}
			logger.InfoC(ctx, "Received versions change from control-plane: %+v", change)

			versions = applyVersionChange(ctx, versions, change)
			ch <- versions
		}
		conn.Close()
	}
}

func applyVersionChange(ctx context.Context, versions model.CpVersionsDto, change model.CpWatcherMessageDto) model.CpVersionsDto {
	if change.State != nil {
		// reset state
		return change.State
	}

	if change.Changes == nil {
		logger.PanicC(ctx, "Unexpected event structure: %+v", change)
	}

	for _, part := range change.Changes {
		found := false
		for i, version := range versions {
			if part.Old != nil && version.Version == part.Old.Version {
				found = true

				if part.New == nil {
					// version is deleted
					versions = remove(versions, i)
				} else {
					// version is updated
					versions[i] = *part.New
				}
				break
			}
		}
		if !found {
			// additive change
			versions = append(versions, *part.New)
		}
	}

	return versions
}

func remove[T any](slice []T, pos int) []T {
	return append(slice[:pos], slice[pos+1:]...)
}

func IsContextCancelled(ctx context.Context) bool {
	return errors.Is(ctx.Err(), context.Canceled)
}

func notifyMaasAboutVersionChanges(ctx context.Context, httpService maasservice.MaaSService, ch <-chan model.CpVersionsDto) {
	defer logger.InfoC(ctx, "Stop b/g version change maas notifier")

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-ch:
			for {
				if IsContextCancelled(ctx) {
					return
				}

				logger.InfoC(ctx, "Send cp versions to maas: %+v", event)
				err := httpService.SendCpVersionsToMaas(ctx, event)
				if err == nil {
					break
				} else {
					logger.ErrorC(ctx, "Error send versions to MaaS: %s Retry in 5sec...", err)
					CancelableSleep(ctx, 5*time.Second)
				}

			}
		}
	}
}

// CancelableSleep
//
//	if timeout has been cancelled via context in the middle of sleep process,
//	then function returns false, otherwise it returns true
func CancelableSleep(ctx context.Context, amount time.Duration) bool {
	select {
	case <-time.After(amount):
		return true
	case <-ctx.Done():
		return false
	}
}
