package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"net/url"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
)

const MaxKubeconfigSize = 1 << 20

type ParsedKubeconfig struct {
	APIServer             string
	ContextName           string
	CAData                []byte
	Token                 string
	ClientCertificateData []byte
	ClientKeyData         []byte
	AuthenticationType    string
}

func ParseKubeconfig(data []byte) (ParsedKubeconfig, error) {
	if len(data) == 0 {
		return ParsedKubeconfig{}, newError(CodeKubeconfigRequired)
	}
	if len(data) > MaxKubeconfigSize {
		return ParsedKubeconfig{}, newError(CodeKubeconfigTooLarge)
	}

	config, err := clientcmd.Load(data)
	if err != nil {
		return ParsedKubeconfig{}, newError(CodeKubeconfigInvalid)
	}
	if strings.TrimSpace(config.CurrentContext) == "" {
		return ParsedKubeconfig{}, newError(CodeCurrentContextMissing)
	}

	contextConfig, ok := config.Contexts[config.CurrentContext]
	if !ok || contextConfig == nil || contextConfig.Cluster == "" || contextConfig.AuthInfo == "" {
		return ParsedKubeconfig{}, newError(CodeCurrentContextInvalid)
	}
	clusterConfig, ok := config.Clusters[contextConfig.Cluster]
	if !ok || clusterConfig == nil {
		return ParsedKubeconfig{}, newError(CodeCurrentContextInvalid)
	}
	authConfig, ok := config.AuthInfos[contextConfig.AuthInfo]
	if !ok || authConfig == nil {
		return ParsedKubeconfig{}, newError(CodeCurrentContextInvalid)
	}

	server := strings.TrimRight(strings.TrimSpace(clusterConfig.Server), "/")
	serverURL, err := url.Parse(server)
	if err != nil || serverURL.Scheme != "https" || serverURL.Host == "" {
		return ParsedKubeconfig{}, newError(CodeHTTPSRequired)
	}
	if clusterConfig.InsecureSkipTLSVerify || clusterConfig.CertificateAuthority != "" || len(clusterConfig.CertificateAuthorityData) == 0 {
		return ParsedKubeconfig{}, newError(CodeInlineCARequired)
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(clusterConfig.CertificateAuthorityData) {
		return ParsedKubeconfig{}, newError(CodeInlineCAInvalid)
	}

	if authConfig.Exec != nil {
		return ParsedKubeconfig{}, newError(CodeExecAuthUnsupported)
	}
	if authConfig.AuthProvider != nil {
		return ParsedKubeconfig{}, newError(CodeAuthProviderUnsupported)
	}
	if authConfig.Username != "" || authConfig.Password != "" {
		return ParsedKubeconfig{}, newError(CodeBasicAuthUnsupported)
	}
	if authConfig.TokenFile != "" || authConfig.ClientCertificate != "" || authConfig.ClientKey != "" {
		return ParsedKubeconfig{}, newError(CodeInlineClientCertRequired)
	}

	token := strings.TrimSpace(authConfig.Token)
	clientCertificateData := authConfig.ClientCertificateData
	clientKeyData := authConfig.ClientKeyData
	authenticationType := ""
	switch {
	case token != "" && len(clientCertificateData) == 0 && len(clientKeyData) == 0:
		authenticationType = "Bearer Token"
	case token == "" && len(clientCertificateData) > 0 && len(clientKeyData) > 0:
		if _, err := tls.X509KeyPair(clientCertificateData, clientKeyData); err != nil {
			return ParsedKubeconfig{}, newError(CodeClientCertificateInvalid)
		}
		authenticationType = "客户端证书"
	case token == "" && len(clientCertificateData) == 0 && len(clientKeyData) == 0:
		return ParsedKubeconfig{}, newError(CodeAuthenticationRequired)
	default:
		return ParsedKubeconfig{}, newError(CodeUnsupportedAuth)
	}

	return ParsedKubeconfig{
		APIServer:             server,
		ContextName:           config.CurrentContext,
		CAData:                append([]byte(nil), clusterConfig.CertificateAuthorityData...),
		Token:                 token,
		ClientCertificateData: append([]byte(nil), clientCertificateData...),
		ClientKeyData:         append([]byte(nil), clientKeyData...),
		AuthenticationType:    authenticationType,
	}, nil
}
