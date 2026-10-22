package cluster

import "errors"

const (
	CodeCollectionLimitExceeded  = "COLLECTION_LIMIT_EXCEEDED"
	CodeNameRequired             = "NAME_REQUIRED"
	CodeEnvironmentRequired      = "ENVIRONMENT_REQUIRED"
	CodeClusterTypeRequired      = "CLUSTER_TYPE_REQUIRED"
	CodeClusterTypeInvalid       = "CLUSTER_TYPE_INVALID"
	CodeClusterTypeMismatch      = "CLUSTER_TYPE_MISMATCH"
	CodeTechnologyStackRequired  = "TECHNOLOGY_STACK_REQUIRED"
	CodeTechnologyStackInvalid   = "TECHNOLOGY_STACK_INVALID"
	CodeKubeconfigRequired       = "KUBECONFIG_REQUIRED"
	CodeKubeconfigTooLarge       = "KUBECONFIG_TOO_LARGE"
	CodeKubeconfigInvalid        = "KUBECONFIG_INVALID"
	CodeCurrentContextMissing    = "CURRENT_CONTEXT_MISSING"
	CodeCurrentContextInvalid    = "CURRENT_CONTEXT_INVALID"
	CodeHTTPSRequired            = "HTTPS_REQUIRED"
	CodeInlineCARequired         = "INLINE_CA_REQUIRED"
	CodeInlineCAInvalid          = "INLINE_CA_INVALID"
	CodeTokenAuthRequired        = "TOKEN_AUTH_REQUIRED"
	CodeUnsupportedAuth          = "UNSUPPORTED_AUTH"
	CodeAuthenticationRequired   = "AUTHENTICATION_REQUIRED"
	CodeInlineClientCertRequired = "INLINE_CLIENT_CERT_REQUIRED"
	CodeClientCertificateInvalid = "CLIENT_CERTIFICATE_INVALID"
	CodeExecAuthUnsupported      = "EXEC_AUTH_UNSUPPORTED"
	CodeAuthProviderUnsupported  = "AUTH_PROVIDER_UNSUPPORTED"
	CodeBasicAuthUnsupported     = "BASIC_AUTH_UNSUPPORTED"
	CodeNameDuplicate            = "NAME_DUPLICATE"
	CodeClusterDuplicate         = "CLUSTER_DUPLICATE"
	CodeClusterNotFound          = "CLUSTER_NOT_FOUND"
	CodeWorkloadNotFound         = "WORKLOAD_NOT_FOUND"
	CodePodNotFound              = "POD_NOT_FOUND"
	CodeInvalidLogTimeRange      = "INVALID_LOG_TIME_RANGE"
	CodePodNotRunning            = "POD_NOT_RUNNING"
	CodeContainerNotFound        = "CONTAINER_NOT_FOUND"
	CodePodExecUnavailable       = "POD_EXEC_UNAVAILABLE"
	CodePodExecForbidden         = "POD_EXEC_FORBIDDEN"
	CodeSearchQueryInvalid       = "SEARCH_QUERY_INVALID"
	CodeEncryptionFailed         = "ENCRYPTION_FAILED"
	CodePersistenceFailed        = "PERSISTENCE_FAILED"
	CodeConnectionTimeout        = "CONNECTION_TIMEOUT"
	CodeConnectionFailed         = "CONNECTION_FAILED"
	CodeTLSVerificationFailed    = "TLS_VERIFICATION_FAILED"
	CodeAuthenticationFailed     = "AUTHENTICATION_FAILED"
	CodeAuthorizationFailed      = "AUTHORIZATION_FAILED"
	CodeCapacityRulesInvalid     = "CAPACITY_RULES_INVALID"
	CodeCapacityPending          = "CAPACITY_PENDING"
	CodeBaselineReasonRequired   = "BASELINE_REASON_REQUIRED"
	CodeSecurityBenchmarkInvalid = "SECURITY_BENCHMARK_INVALID"
	CodeSecurityWhitelistInvalid = "SECURITY_WHITELIST_INVALID"
	CodeSecurityScanModeInvalid  = "SECURITY_SCAN_MODE_INVALID"
	CodeSecurityScanQueueFull    = "SECURITY_SCAN_QUEUE_FULL"
	CodeSecurityScanInProgress   = "SECURITY_SCAN_IN_PROGRESS"
	CodeSecurityScanUnavailable  = "SECURITY_SCAN_UNAVAILABLE"
)

type Error struct {
	Code string
}

func (err *Error) Error() string {
	return err.Code
}

func newError(code string) error {
	return &Error{Code: code}
}

func ErrorCode(err error) string {
	var clusterError *Error
	if errors.As(err, &clusterError) {
		return clusterError.Code
	}
	return CodeConnectionFailed
}
