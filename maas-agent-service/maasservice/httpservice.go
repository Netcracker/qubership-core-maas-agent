package maasservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/controller"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/httputils"
	"github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/model"

	"github.com/netcracker/qubership-core-lib-go/v3/logging"
)

const MAAS_BG_STATUS_API = "/api/v1/bg-status"
const MAAS_SYNCHRONIZE_TENANTS_API = "/api/v1/synchronize-tenants"

type MaaSService struct {
	MaasAddr                   string
	MaasEnabled                bool
	CpAddr                     *url.URL
	TmAddr                     *url.URL
	Namespace                  string
	BasicRequestCreator        func(method, url string) *httputils.HttpRequest
	M2MRequestCreator          func(method, url string) *httputils.HttpRequest
	DrMode                     string
	CompositeIsolationDisabled bool
}

var log logging.Logger

func init() {
	log = logging.GetLogger("controller")
}

func (s MaaSService) SendActiveTenants(ctx context.Context, tenants string) error {
	code, body, err := s.BasicRequestCreator(http.MethodPost, s.MaasAddr+MAAS_SYNCHRONIZE_TENANTS_API).
		SetRequestBody(&tenants).
		SetHeader(controller.HTTP_X_ORIGIN_NAMESPACE, s.Namespace).
		Execute(ctx)

	if err != nil {
		err = fmt.Errorf("error sending maas list of active tenants: %v", err)
		log.ErrorC(ctx, err.Error())
		return err
	}

	switch code {
	case http.StatusOK:
		log.InfoC(ctx, "Request to MaaS completed successfully, resp code: %v", http.StatusOK)
		return nil
	default:
		err := fmt.Errorf("unexpected status code in response: expected %v, but got %v, body: %s", http.StatusOK, code, body)
		log.ErrorC(ctx, "%s", err.Error())
		return err
	}
}

func (s MaaSService) SendCpVersionsToMaas(ctx context.Context, message model.CpVersionsDto) error {
	reqBody, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("error marshalling message '%v', err : %w", message, err)
	}

	code, body, err := s.BasicRequestCreator(http.MethodPost, s.MaasAddr+MAAS_BG_STATUS_API).
		SetHeader(controller.HTTP_X_ORIGIN_NAMESPACE, s.Namespace).
		SetRequestBodyBytes(reqBody).
		Execute(ctx)
	if err != nil {
		return fmt.Errorf("error during sending request to MaaS to send cp message: %w", err)
	}

	switch code {
	case http.StatusOK:
		return nil
	default:
		return fmt.Errorf("unexpected status code in response: expected %v, but got %v, body: %s", http.StatusOK, code, body)
	}
}

// SynchronizeTenantsToMaaS
//
//	maas-agent sends active tenants to MaaS during initialisation
func (s MaaSService) SynchronizeTenantsToMaaS(ctx context.Context) {
	ok := false
	var tenantsStr string

	// send request until stable connection to tenant-manager
	for i := 0; i < 15*60/5 && !ok; i++ {
		code, body, err := s.M2MRequestCreator(http.MethodGet, s.TmAddr.String()).Execute(ctx)
		if err != nil {
			log.ErrorC(ctx, "Error connecting to tenant-manager: %v", err)
			log.WarnC(ctx, "Sleeping for 5 sec and retry operation...")
			time.Sleep(5 * time.Second)
			continue
		}

		if code != http.StatusOK {
			log.ErrorC(ctx, "Tenant manager response is not 200: %v", code)
			log.WarnC(ctx, "Sleeping for 5 sec and retry operation...")
			time.Sleep(5 * time.Second)
			continue
		}

		tenantsStr = string(body)
		log.InfoC(ctx, "Tenant manager get active tenants response: %v", tenantsStr)
		ok = true
	}

	if !ok {
		log.PanicC(ctx, "Timeout reached trying get correct response from tenant-manager. See error messages above")
	}

	if err := s.SendActiveTenants(ctx, tenantsStr); err != nil {
		log.PanicC(ctx, "Error sending tenant list to maas: %v", err)
	}

	log.InfoC(ctx, "Successfully synchronize tenants with maas")
}
