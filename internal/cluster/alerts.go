package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Alert is the bounded subset of the Alertmanager v2 response retained by the platform.
type Alert struct {
	Fingerprint string            `json:"fingerprint"`
	Labels      map[string]string `json:"labels"`
	Summary     string            `json:"summary"`
	Description string            `json:"description"`
	RunbookURL  string            `json:"runbook_url"`
	RuleURL     string            `json:"rule_url"`
	StartsAt    time.Time         `json:"starts_at"`
	EndsAt      *time.Time        `json:"ends_at,omitempty"`
	State       string            `json:"state"`
}

type AlertSnapshot struct {
	Source      string    `json:"source"`
	CollectedAt time.Time `json:"collected_at"`
	Alerts      []Alert   `json:"alerts"`
}

func (service *OnboardingService) GetClusterAlerts(ctx context.Context, id string) (AlertSnapshot, error) {
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return AlertSnapshot{}, err
	}
	return service.alertsForRecord(ctx, record)
}

func (service *OnboardingService) alertsForRecord(ctx context.Context, record ClusterRecord) (AlertSnapshot, error) {
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return AlertSnapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	client, err := NewKubernetesConnector(10 * time.Second).listClientset(parsed)
	if err != nil {
		return AlertSnapshot{}, err
	}
	services, err := boundedServices(ctx, client)
	if err != nil {
		return AlertSnapshot{}, monitoringConnectionError(err)
	}
	for _, source := range discoverAlertmanagerServices(services.Items) {
		stream, err := client.CoreV1().RESTClient().Get().Namespace(source.namespace).Resource("services").Name(source.proxyName).SubResource("proxy").Suffix("api", "v2", "alerts").Param("active", "true").Param("silenced", "true").Param("inhibited", "true").Stream(ctx)
		if err != nil {
			if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
				return AlertSnapshot{}, monitoringConnectionError(err)
			}
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(stream, 2<<20+1))
		_ = stream.Close()
		if readErr != nil || len(raw) > 2<<20 {
			return AlertSnapshot{}, errors.New("Alertmanager 告警响应过大或读取失败")
		}
		var items []struct {
			Fingerprint string            `json:"fingerprint"`
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
			StartsAt    time.Time         `json:"startsAt"`
			EndsAt      *time.Time        `json:"endsAt"`
			Status      struct {
				State string `json:"state"`
			} `json:"status"`
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			return AlertSnapshot{}, fmt.Errorf("Alertmanager 返回内容无效: %w", err)
		}
		if len(items) > 5000 {
			return AlertSnapshot{}, errors.New("Alertmanager 告警超过单次上限 5000 条")
		}
		result := AlertSnapshot{Source: source.namespace + "/" + source.name, CollectedAt: time.Now().UTC(), Alerts: make([]Alert, 0, len(items))}
		for _, item := range items {
			labels := make(map[string]string, 8)
			for _, key := range []string{"alertname", "severity", "namespace", "pod", "node", "instance", "deployment", "statefulset", "daemonset", "replicaset", "service", "container", "job", "prometheus", "alertstate"} {
				if value := item.Labels[key]; value != "" {
					labels[key] = truncateAlert(value, 200)
				}
			}
			fingerprint := item.Fingerprint
			if fingerprint == "" {
				sum := sha256.Sum256(rawLabels(item.Labels))
				fingerprint = hex.EncodeToString(sum[:8])
			}
			if len(fingerprint) > 128 {
				fingerprint = fingerprint[:128]
			}
			runbook := item.Annotations["runbook_url"]
			if runbook == "" {
				runbook = item.Annotations["runbookUrl"]
			}
			result.Alerts = append(result.Alerts, Alert{
				Fingerprint: fingerprint,
				Labels:      labels,
				Summary:     truncateAlert(item.Annotations["summary"], 500),
				Description: truncateAlert(item.Annotations["description"], 2000),
				RunbookURL:  truncateAlert(runbook, 1024),
				RuleURL:     truncateAlert(item.Annotations["prometheus"], 1024),
				StartsAt:    item.StartsAt,
				EndsAt:      item.EndsAt,
				State:       item.Status.State,
			})
		}
		return result, nil
	}
	return AlertSnapshot{}, &MonitoringError{Code: "ALERTMANAGER_NOT_FOUND", Message: "该集群未发现可访问的 Alertmanager Service，或 Service 代理不可用。"}
}

type alertmanagerService struct {
	namespace, name, proxyName string
	score                      int
}

func discoverAlertmanagerServices(services []corev1.Service) []alertmanagerService {
	result := []alertmanagerService{}
	for _, service := range services {
		identity := strings.ToLower(service.Name + "/" + service.Labels["app"] + "/" + service.Labels["app.kubernetes.io/name"])
		if !strings.Contains(identity, "alertmanager") {
			continue
		}
		for _, port := range service.Spec.Ports {
			if port.Port != 9093 && !strings.Contains(strings.ToLower(port.Name), "web") && !strings.Contains(strings.ToLower(port.Name), "http") {
				continue
			}
			portName := port.Name
			if portName == "" {
				portName = strconv.Itoa(int(port.Port))
			}
			scheme := "http"
			if port.Port == 443 || strings.Contains(strings.ToLower(portName), "https") {
				scheme = "https"
			}
			score := 0
			if port.Port == 9093 {
				score += 3
			}
			if strings.Contains(strings.ToLower(service.Namespace), "monitoring") {
				score += 2
			}
			result = append(result, alertmanagerService{service.Namespace, service.Name, fmt.Sprintf("%s:%s:%s", scheme, service.Name, portName), score})
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].score > result[j].score })
	return result
}

func rawLabels(labels map[string]string) []byte { raw, _ := json.Marshal(labels); return raw }
func truncateAlert(value string, max int) string {
	if len(value) > max {
		return value[:max]
	}
	return value
}
