package setup

import (
	"context"
	"fmt"
	"os"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func RunCertificateRenewal(ctx context.Context) error {
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "ftc-platform"
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, platformConfigMap, metav1.GetOptions{})
	if err != nil {
		return err
	}
	domain, err := ValidateInternalDomain(cm.Data["PUBLIC_HOSTNAME"])
	if err != nil {
		return err
	}
	// Renewal must never create a new CA: clients explicitly trust the old root.
	if _, err := client.CoreV1().Secrets(namespace).Get(ctx, platformCASecret, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("stored CA is required for renewal: %w", err)
	}
	server := &Server{config: Config{Namespace: namespace}, client: client}
	_, err = server.EnsureTLS(ctx, platformCASecret, platformTLSCert, domain, nil)
	return err
}

func buildCertificateCronJob(plan RuntimePlan) *batchv1.CronJob {
	namespace := runtimeNamespace(plan)
	job := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform-certificates", Namespace: namespace},
		Spec: batchv1.CronJobSpec{
			Schedule:                   "0 3 * * 0",
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			SuccessfulJobsHistoryLimit: int32Pointer(1),
			FailedJobsHistoryLimit:     int32Pointer(2),
			JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				ServiceAccountName: "ftc-platform-certificates",
				RestartPolicy:      corev1.RestartPolicyOnFailure,
				ImagePullSecrets:   append([]corev1.LocalObjectReference(nil), plan.ImagePullSecrets...),
				SecurityContext:    &corev1.PodSecurityContext{RunAsNonRoot: boolPointer(true), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
				Containers:         []corev1.Container{{Name: "renew", Image: strings.TrimSpace(plan.PlatformImage), Command: []string{"/app/ftc-platform", "renew-certificate"}, Env: []corev1.EnvVar{{Name: "POD_NAMESPACE", Value: namespace}}, SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: boolPointer(false), ReadOnlyRootFilesystem: boolPointer(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}}},
			}}}},
		},
	}
	if plan.ImagePullSecret != "" {
		job.Spec.JobTemplate.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: plan.ImagePullSecret}}
	}
	return job
}

func (server *Server) applyCertificateCronJob(ctx context.Context, plan RuntimePlan) error {
	jobs := server.client.BatchV1().CronJobs(runtimeNamespace(plan))
	desired := buildCertificateCronJob(plan)
	current, err := jobs.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = jobs.Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	desired.ResourceVersion = current.ResourceVersion
	_, err = jobs.Update(ctx, desired, metav1.UpdateOptions{})
	return err
}

func int32Pointer(value int32) *int32 { return &value }
