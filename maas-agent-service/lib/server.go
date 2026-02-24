package bin

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/bg"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/controller"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/httputils"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/maasservice"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/model"

	"github.com/gofiber/fiber/v3"
	"github.com/netcracker/qubership-core-lib-go-actuator-common/v2/health"
	"github.com/netcracker/qubership-core-lib-go-actuator-common/v2/tracing"
	fiberserver "github.com/netcracker/qubership-core-lib-go-fiber-server-utils/v2"
	"github.com/netcracker/qubership-core-lib-go-fiber-server-utils/v2/server"
	"github.com/netcracker/qubership-core-lib-go-rest-utils/v2/consul-propertysource"
	routeregistration "github.com/netcracker/qubership-core-lib-go-rest-utils/v2/route-registration"
	"github.com/netcracker/qubership-core-lib-go/v3/configloader"
	constants "github.com/netcracker/qubership-core-lib-go/v3/const"
	"github.com/netcracker/qubership-core-lib-go/v3/logging"
	"github.com/netcracker/qubership-core-lib-go/v3/security"
	"github.com/netcracker/qubership-core-lib-go/v3/serviceloader"
)

var (
	logger      = logging.GetLogger("server")
	exitCode    atomic.Int32
	ctx, cancel = context.WithCancel(context.Background())
)

func init() {
	consulPS := consul.NewLoggingPropertySource()
	propertySources := consul.AddConsulPropertySource(configloader.BasePropertySources())
	configloader.InitWithSourcesArray(append(propertySources, consulPS))
	consul.StartWatchingForPropertiesWithRetry(context.Background(), consulPS, func(event interface{}, err error) {})
}

func initConfiguration(ctx context.Context) maasservice.MaaSService {
	serviceName := configloader.GetKoanf().MustString("microservice.name")
	namespace := configloader.GetKoanf().MustString("microservice.namespace")

	cpDefaultUrl := constants.SelectUrl("http://control-plane:8080", "https://control-plane:8443")
	cpAddrStr := configloader.GetOrDefaultString("apigateway.control-plane.url", cpDefaultUrl)
	cpAddrUrl, err := url.ParseRequestURI(cpAddrStr)
	if err != nil {
		logger.Panic("Error parsing control-plane address: `%s`, error: %s", cpAddrStr, err)
	}

	maasEnabled := configloader.GetKoanf().Bool("maas.enabled")
	maasInternalAddr := "<maas-not-enabled>"
	if maasEnabled {
		maasInternalAddr = configloader.GetKoanf().MustString("maas.internal.address")
		if _, err := url.ParseRequestURI(maasInternalAddr); err != nil {
			logger.Panic("MaaS address should be a valid url: `%s`, error: %v", maasInternalAddr, err)
		}
	}

	username := configloader.GetKoanf().MustString("maas.agent.credentials.username")
	password := configloader.GetKoanf().MustString("maas.agent.credentials.password")

	tmDefaultUrl := constants.SelectUrl("http://tenant-manager:8080", "https://tenant-manager:8443") +
		"/api/v4/tenant-manager/manage/tenants?search=status=ACTIVE"
	tmAddrStr := configloader.GetOrDefaultString("apigateway.tenant-manager.url", tmDefaultUrl)
	tmAddrUrl, err := url.ParseRequestURI(tmAddrStr)
	if err != nil {
		logger.Panic("Error parsing tenant-manager address: `%s`, error: %s", tmAddrStr, err)
	}

	drMode := configloader.GetKoanf().MustString("execution.mode")

	compositeIsolationDisabled := !configloader.GetKoanf().Bool("maas.agent.namespace.isolation.enabled")
	logger.InfoC(ctx, "Composite namespace isolation mode is: %v", iif(compositeIsolationDisabled, "disabled", "enabled"))

	// TODO deprecate and remove this registration
	routeregistration.NewRegistrar().WithRoutes(
		routeregistration.Route{
			From:      "/api/v1/" + serviceName,
			To:        "/api/v1",
			RouteType: routeregistration.Internal,
		},
	).Register()

	tokenProvider := serviceloader.MustLoad[security.TokenProvider]()
	return maasservice.MaaSService{
		CpAddr:                     cpAddrUrl,
		TmAddr:                     tmAddrUrl,
		MaasAddr:                   maasInternalAddr,
		MaasEnabled:                maasEnabled,
		Namespace:                  namespace,
		DrMode:                     drMode,
		CompositeIsolationDisabled: compositeIsolationDisabled,
		BasicRequestCreator: func(method, url string) *httputils.HttpRequest {
			return httputils.Req(method, url,
				model.AuthCredentials{Username: username, Password: password}.AuthHeaderProvider,
			)
		},
		M2MRequestCreator: func(method, url string) *httputils.HttpRequest {
			return httputils.Req(method, url, func(ctx context.Context) (string, error) {
				if token, err := tokenProvider.GetToken(ctx); err == nil {
					return "Bearer " + token, nil
				} else {
					return "", fmt.Errorf("error get m2m token: %w", err)
				}
			})
		},
	}
}

func iif[T any](clause bool, onTrue T, onFalse T) T {
	if clause {
		return onTrue
	} else {
		return onFalse
	}
}

func RunServer() {
	maasService := initConfiguration(ctx)
	healthService, err := health.NewHealthService()
	if err != nil {
		logger.PanicC(ctx, "Couldn't create healthService")
	}

	fiberCfg := fiber.Config{
		Network:      fiber.NetworkTCP,
		IdleTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		ReadTimeout:  130 * time.Second,
	}
	app, err := fiberserver.New(fiberCfg).
		WithPrometheus("/prometheus").
		WithPprof("6060").
		WithHealth("/health", healthService).
		WithTracer(tracing.NewZipkinTracer()).
		WithLogLevelsInfo().
		ProcessWithContext(ctx)
	if err != nil {
		logger.ErrorC(ctx, "Error while create app because: "+err.Error())
		return
	}

	var requestHandler func(*fiber.Ctx) error
	logger.InfoC(ctx, "Initialize agent with MAAS_ENABLED=%s, DR_MODE=%s", maasService.MaasEnabled, maasService.DrMode)
	if maasService.MaasEnabled {
		// disaster recovery check
		if maasService.DrMode == "active" {
			logger.InfoC(ctx, "Starting in normal proxy mode to mass: %s", maasService.MaasAddr)
			go bg.SubscribeToControlPlaneWatcher(ctx, maasService)
			go maasService.SynchronizeTenantsToMaaS(ctx)
		} else {
			logger.WarnC(ctx, "Disaster recovery mode is not 'active', but '%s'. Not sending "+
				"requests to tenant-manager and control-plane. Only GET operations will be supported by MaaS", maasService.DrMode)
		}
		tokenProvider := serviceloader.MustLoad[security.TokenProvider]()

		apiController := &controller.ApiHttpHandler{
			BasicRequestCreator:        maasService.BasicRequestCreator,
			MaasAddr:                   maasService.MaasAddr,
			Namespace:                  maasService.Namespace,
			TokenValidator:             tokenProvider.ValidateToken,
			CompositeIsolationDisabled: maasService.CompositeIsolationDisabled,
		}
		requestHandler = apiController.ProcessRequest
	} else {
		logger.WarnC(ctx, "MAAS_ENABLED property is set to `false'. Starting in stub mode")
		requestHandler = func(c *fiber.Ctx) error {
			return fiber.NewError(fiber.StatusBadGateway, "Environment variable MAAS_ENABLED=false or was not set during deployment, MaaS agent is working in stub mode")
		}
	}

	app.All("/api/*", requestHandler)
	app.Get("/api-version", requestHandler)

	registerShutdownHook(func(code int) {
		// We received an interrupt signal, shut down.
		if err := app.Shutdown(); err != nil {
			// Error from closing listeners, or context timeout:
			logger.Error("maas-agent error during server Shutdown: %v", err)
		}
		cancel()

		// save exit code to be used in Exit() call
		exitCode.Store(int32(code))
	})

	server.StartServer(app, "http.server.bind")

	logger.InfoC(ctx, "maas-agent server gracefully finished")
	os.Exit(int(exitCode.Load()))
}

func registerShutdownHook(hook func(exitCode int)) {
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)    // Ctrl+C
		signal.Notify(sigint, syscall.SIGTERM) // k8s pre-termination notification

		signal := <-sigint
		logger.Info("OS signal '%s' received, starting shutdown", (signal).String())

		exitCode := 0
		switch signal {
		case syscall.SIGINT:
			exitCode = 130
		case syscall.SIGTERM:
			exitCode = 143
		}
		hook(exitCode)
	}()
}
