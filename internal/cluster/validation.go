package cluster

import (
	"context"
	"strings"
)

type ValidationPreview struct {
	Name               string `json:"name"`
	APIServer          string `json:"api_server"`
	ContextName        string `json:"context_name"`
	KubernetesVersion  string `json:"kubernetes_version"`
	AuthenticationType string `json:"authentication_type"`
}

type ValidationService struct {
	connector Connector
}

func NewValidationService(connector Connector) *ValidationService {
	return &ValidationService{connector: connector}
}

func (service *ValidationService) Validate(ctx context.Context, name string, data []byte) (ValidationPreview, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ValidationPreview{}, newError(CodeNameRequired)
	}
	parsed, err := ParseKubeconfig(data)
	if err != nil {
		return ValidationPreview{}, err
	}
	version, err := service.connector.Version(ctx, parsed)
	if err != nil {
		return ValidationPreview{}, err
	}
	return ValidationPreview{
		Name:               name,
		APIServer:          parsed.APIServer,
		ContextName:        parsed.ContextName,
		KubernetesVersion:  version,
		AuthenticationType: parsed.AuthenticationType,
	}, nil
}
