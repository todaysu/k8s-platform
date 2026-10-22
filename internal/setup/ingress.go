package setup

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type IngressPlan struct {
	Namespace    string
	Host         string
	IngressClass string
	ServiceName  string
	ServicePort  int32
}

func BuildIngress(plan IngressPlan) (*networkingv1.Ingress, error) {
	host, err := ValidateInternalDomain(plan.Host)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(plan.IngressClass) == "" {
		return nil, fmt.Errorf("IngressClass 不能为空")
	}
	if plan.Namespace == "" {
		plan.Namespace = "ftc-platform"
	}
	if plan.ServiceName == "" {
		plan.ServiceName = "ftc-platform"
	}
	if plan.ServicePort == 0 {
		plan.ServicePort = 80
	}
	pathType := networkingv1.PathTypePrefix
	class := plan.IngressClass
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ftc-platform",
			Namespace: plan.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/part-of": "ftc-platform"},
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/proxy-read-timeout": "3600",
				"nginx.ingress.kubernetes.io/proxy-send-timeout": "3600",
				"nginx.ingress.kubernetes.io/proxy-buffering":    "off",
				"nginx.ingress.kubernetes.io/proxy-body-size":    "4m",
				"nginx.ingress.kubernetes.io/ssl-redirect":       "true",
				"nginx.ingress.kubernetes.io/force-ssl-redirect": "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &class,
			TLS:              []networkingv1.IngressTLS{{Hosts: []string{host}, SecretName: platformTLSCert}},
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: plan.ServiceName,
									Port: networkingv1.ServiceBackendPort{Number: plan.ServicePort},
								},
							},
						}},
					},
				},
			}},
		},
	}, nil
}

// ApplyIngress creates the private CA, the TLS Secret and the Ingress. It
// never rotates an existing CA or an existing certificate for another host.
func (server *Server) ApplyIngress(ctx context.Context, plan IngressPlan) error {
	if server.client == nil {
		return fmt.Errorf("初始化控制器未连接 Kubernetes API")
	}
	if plan.Namespace == "" {
		plan.Namespace = "ftc-platform"
	}
	ingress, err := BuildIngress(plan)
	if err != nil {
		return err
	}
	host := ingress.Spec.Rules[0].Host

	secrets := server.client.CoreV1().Secrets(plan.Namespace)
	tlsSecret, err := secrets.Get(ctx, platformTLSCert, metav1.GetOptions{})
	switch {
	case err == nil:
		certificate, parseErr := parseServerCertificate(tlsSecret.Data[corev1.TLSCertKey])
		if parseErr != nil {
			return fmt.Errorf("现有 TLS Secret 无法解析：%w", parseErr)
		}
		if !containsName(certificate.DNSNames, host) {
			return fmt.Errorf("现有 TLS 证书不包含域名 %s；请先备份并删除 %s 后重试", host, platformTLSCert)
		}
	case apierrors.IsNotFound(err):
		bundle, generateErr := GenerateSelfSignedTLS(host)
		if generateErr != nil {
			return generateErr
		}
		if _, err := secrets.Get(ctx, platformCASecret, metav1.GetOptions{}); err == nil {
			return fmt.Errorf("CA Secret 已存在但 TLS Secret 缺失；为避免重建信任根，请恢复 TLS Secret 后重试")
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("读取 CA Secret 失败：%w", err)
		}
		if _, err := secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platformCASecret, Namespace: plan.Namespace, Labels: map[string]string{"app.kubernetes.io/part-of": "ftc-platform"}},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{"ca.crt": bundle.CACert, "ca.key": bundle.CAKey},
		}, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("创建 CA Secret 失败：%w", err)
		}
		if _, err := secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platformTLSCert, Namespace: plan.Namespace, Labels: map[string]string{"app.kubernetes.io/part-of": "ftc-platform"}},
			Type:       corev1.SecretTypeTLS,
			Data: map[string][]byte{
				corev1.TLSCertKey:       bundle.ServerCert,
				corev1.TLSPrivateKeyKey: bundle.ServerKey,
			},
		}, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("创建 TLS Secret 失败：%w", err)
		}
	default:
		return fmt.Errorf("读取 TLS Secret 失败：%w", err)
	}

	ingresses := server.client.NetworkingV1().Ingresses(plan.Namespace)
	existing, err := ingresses.Get(ctx, ingress.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		if _, err := ingresses.Create(ctx, ingress, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("创建 Ingress 失败：%w", err)
		}
	case err != nil:
		return fmt.Errorf("读取 Ingress 失败：%w", err)
	default:
		ingress.ResourceVersion = existing.ResourceVersion
		if _, err := ingresses.Update(ctx, ingress, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("更新 Ingress 失败：%w", err)
		}
	}
	return nil
}

func parseServerCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("证书不是 PEM 格式")
	}
	return x509.ParseCertificate(block.Bytes)
}

func containsName(names []string, target string) bool {
	for _, name := range names {
		if strings.EqualFold(name, target) {
			return true
		}
	}
	return false
}

func (server *Server) handleCA(writer http.ResponseWriter, request *http.Request) {
	namespace := server.config.Namespace
	if namespace == "" {
		namespace = "ftc-platform"
	}
	if server.client == nil {
		http.Error(writer, "setup controller is not connected to Kubernetes", http.StatusServiceUnavailable)
		return
	}
	secret, err := server.client.CoreV1().Secrets(namespace).Get(request.Context(), platformCASecret, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		http.Error(writer, "CA certificate is not available", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(writer, "failed to read CA certificate", http.StatusInternalServerError)
		return
	}
	ca := secret.Data["ca.crt"]
	if len(ca) == 0 {
		http.Error(writer, "CA certificate is empty", http.StatusNotFound)
		return
	}
	writer.Header().Set("Content-Type", "application/x-pem-file")
	writer.Header().Set("Content-Disposition", `attachment; filename="ftc-platform-ca.crt"`)
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(ca)
}
