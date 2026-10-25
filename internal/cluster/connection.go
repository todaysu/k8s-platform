package cluster

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/yaml"
)

// cachedListOptions reads from the API server watch cache instead of issuing a
// quorum read to etcd for every snapshot refresh. Health and capacity tolerate
// a slightly stale view; detail pages still get a consistent object on write.
var cachedListOptions = metav1.ListOptions{ResourceVersion: "0"}

type Connector interface {
	Version(context.Context, ParsedKubeconfig) (string, error)
	Health(context.Context, ParsedKubeconfig) (HealthSnapshot, error)
	Nodes(context.Context, ParsedKubeconfig) ([]ClusterNode, error)
	Pod(context.Context, ParsedKubeconfig, string, string) (ClusterPodDetail, error)
	PodLogs(context.Context, ParsedKubeconfig, string, string, PodLogOptions) (PodLogSnapshot, error)
	Pods(context.Context, ParsedKubeconfig) ([]ClusterPod, error)
	Workloads(context.Context, ParsedKubeconfig) ([]ClusterWorkload, error)
	Workload(context.Context, ParsedKubeconfig, string, string, string) (ClusterWorkloadDetail, error)
}

type EventConnector interface {
	ListEvents(context.Context, ParsedKubeconfig) (EventBatch, error)
	WatchEvents(context.Context, ParsedKubeconfig, string, string, func(ClusterEvent) error) error
}

type PodExecConnector interface {
	ExecPod(context.Context, ParsedKubeconfig, string, string, PodExecOptions) error
}

type WorkloadYAMLConnector interface {
	WorkloadYAML(context.Context, ParsedKubeconfig, string, string, string) (WorkloadYAML, error)
}

type CapacityConnector interface {
	Capacity(context.Context, ParsedKubeconfig, CapacityRule) (ClusterCapacity, error)
}

func (connector *KubernetesConnector) Capacity(ctx context.Context, parsed ParsedKubeconfig, rule CapacityRule) (ClusterCapacity, error) {
	clientset, err := connector.listClientset(parsed)
	if err != nil {
		return ClusterCapacity{}, err
	}

	businessSet := make(map[string]struct{}, len(rule.BusinessNamespaces))
	for _, namespace := range rule.BusinessNamespaces {
		businessSet[namespace] = struct{}{}
	}
	snapshot := ClusterCapacity{
		CollectedAt:             time.Now().UTC(),
		SystemNamespaces:        []string{},
		ClassificationVersionID: rule.VersionID,
		SafetyReservePercent:    15,
		Comparison:              CapacityComparison{Status: "not_set"},
	}
	if err := visitNodes(ctx, clientset, func(item *corev1.Node) error {
		node := *item
		if node.Spec.Unschedulable {
			return nil
		}
		snapshot.WorkerNodes++
		snapshot.CPUAllocatableMilli += node.Status.Allocatable.Cpu().MilliValue()
		snapshot.MemoryAllocatable += node.Status.Allocatable.Memory().Value()
		return nil
	}); err != nil {
		return ClusterCapacity{}, err
	}

	byNamespace := make(map[string]*NamespaceCapacity)
	ensureNamespace := func(name string) *NamespaceCapacity {
		if entry := byNamespace[name]; entry != nil {
			return entry
		}
		class := "platform"
		if _, ok := businessSet[name]; ok {
			class = "business"
		} else {
			snapshot.SystemNamespaces = append(snapshot.SystemNamespaces, name)
		}
		entry := &NamespaceCapacity{Namespace: name, Class: class}
		byNamespace[name] = entry
		return entry
	}
	// Namespace membership is independent of whether it currently contains scheduled Pods.
	if err := visitNamespaces(ctx, clientset, func(item *corev1.Namespace) error {
		ensureNamespace(item.Name).CreatedAt = item.CreationTimestamp.Time
		return nil
	}); err != nil {
		return ClusterCapacity{}, err
	}

	if err := visitPods(ctx, clientset, "", func(item *corev1.Pod) error {
		pod := *item
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			return nil
		}
		values := effectivePodResources(pod)
		if values.CPURequestsMilli == 0 {
			values.PodsWithoutCPURequests = 1
		}
		if values.MemoryRequests == 0 {
			values.PodsWithoutMemoryRequests = 1
		}
		if pod.Spec.NodeName == "" {
			addCapacityValues(&snapshot.Pending, values)
			return nil
		}
		entry := ensureNamespace(pod.Namespace)
		target := &snapshot.Platform
		if entry.Class == "business" {
			target = &snapshot.Business
		}
		addCapacityValues(target, values)
		entry.Pods++
		addCapacityValues(&entry.CapacityValues, values)
		return nil
	}); err != nil {
		return ClusterCapacity{}, err
	}

	sort.Strings(snapshot.SystemNamespaces)
	for _, entry := range byNamespace {
		snapshot.Namespaces = append(snapshot.Namespaces, *entry)
	}
	sort.Slice(snapshot.Namespaces, func(left, right int) bool {
		leftScore := snapshot.Namespaces[left].MemoryRequests + snapshot.Namespaces[left].CPURequestsMilli*1024*1024
		rightScore := snapshot.Namespaces[right].MemoryRequests + snapshot.Namespaces[right].CPURequestsMilli*1024*1024
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return snapshot.Namespaces[left].Namespace < snapshot.Namespaces[right].Namespace
	})

	services, err := boundedServices(ctx, clientset)
	if err == nil {
		connector.collectCapacityUsage(ctx, clientset, services.Items, rule, &snapshot)
	}
	return snapshot, nil
}

func effectivePodResources(pod corev1.Pod) CapacityValues {
	regular := CapacityValues{}
	for _, container := range pod.Spec.Containers {
		addResourceList(&regular, container.Resources.Requests, container.Resources.Limits)
	}
	initMaximum := CapacityValues{}
	for _, container := range pod.Spec.InitContainers {
		candidate := CapacityValues{}
		addResourceList(&candidate, container.Resources.Requests, container.Resources.Limits)
		initMaximum.CPURequestsMilli = maxInt64(initMaximum.CPURequestsMilli, candidate.CPURequestsMilli)
		initMaximum.CPULimitsMilli = maxInt64(initMaximum.CPULimitsMilli, candidate.CPULimitsMilli)
		initMaximum.MemoryRequests = maxInt64(initMaximum.MemoryRequests, candidate.MemoryRequests)
		initMaximum.MemoryLimits = maxInt64(initMaximum.MemoryLimits, candidate.MemoryLimits)
	}
	regular.CPURequestsMilli = maxInt64(regular.CPURequestsMilli, initMaximum.CPURequestsMilli)
	regular.CPULimitsMilli = maxInt64(regular.CPULimitsMilli, initMaximum.CPULimitsMilli)
	regular.MemoryRequests = maxInt64(regular.MemoryRequests, initMaximum.MemoryRequests)
	regular.MemoryLimits = maxInt64(regular.MemoryLimits, initMaximum.MemoryLimits)
	if pod.Spec.Overhead != nil {
		regular.CPURequestsMilli += pod.Spec.Overhead.Cpu().MilliValue()
		regular.MemoryRequests += pod.Spec.Overhead.Memory().Value()
	}
	return regular
}

func addResourceList(target *CapacityValues, requests corev1.ResourceList, limits corev1.ResourceList) {
	target.CPURequestsMilli += requests.Cpu().MilliValue()
	target.MemoryRequests += requests.Memory().Value()
	target.CPULimitsMilli += limits.Cpu().MilliValue()
	target.MemoryLimits += limits.Memory().Value()
}

func addCapacityValues(target *CapacityValues, value CapacityValues) {
	target.CPURequestsMilli += value.CPURequestsMilli
	target.CPULimitsMilli += value.CPULimitsMilli
	target.MemoryRequests += value.MemoryRequests
	target.MemoryLimits += value.MemoryLimits
	target.PodsWithoutCPURequests += value.PodsWithoutCPURequests
	target.PodsWithoutMemoryRequests += value.PodsWithoutMemoryRequests
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func exactNamespacePattern(namespaces []string) string {
	if len(namespaces) == 0 {
		return "a^"
	}
	parts := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		parts = append(parts, regexp.QuoteMeta(namespace))
	}
	return "^(?:" + strings.Join(parts, "|") + ")$"
}

func (connector *KubernetesConnector) collectCapacityUsage(ctx context.Context, clientset *kubernetes.Clientset, services []corev1.Service, rule CapacityRule, snapshot *ClusterCapacity) {
	businessPattern := exactNamespacePattern(rule.BusinessNamespaces)
	// Platform usage covers every namespace outside the business scope, so it
	// is expected to return data whenever workloads exist. Business usage is
	// legitimately empty when no business namespace is configured or when no
	// Pod is running in that scope, so an empty sum is a valid zero rather
	// than a collection failure.
	platformQueries := []string{
		fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{container!="",container!="POD",namespace!="",namespace!~%q}[5m]))`, businessPattern),
		fmt.Sprintf(`sum(container_memory_working_set_bytes{container!="",container!="POD",namespace!="",namespace!~%q})`, businessPattern),
	}
	businessQueries := []string{
		fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{container!="",container!="POD",namespace=~%q}[5m]))`, businessPattern),
		fmt.Sprintf(`sum(container_memory_working_set_bytes{container!="",container!="POD",namespace=~%q})`, businessPattern),
	}
	for _, candidate := range discoverPrometheusServices(services) {
		platformCPU, ok := queryPrometheus(ctx, clientset, candidate, platformQueries[0])
		if !ok {
			continue
		}
		platformMemory, ok := queryPrometheus(ctx, clientset, candidate, platformQueries[1])
		if !ok {
			continue
		}
		// Missing business data must not discard the platform values that were
		// already collected successfully.
		businessCPU, _ := queryPrometheusSum(ctx, clientset, candidate, businessQueries[0])
		businessMemory, _ := queryPrometheusSum(ctx, clientset, candidate, businessQueries[1])
		platformCPUValue, businessCPUValue := int64(math.Round(platformCPU*1000)), int64(math.Round(businessCPU*1000))
		platformMemoryValue, businessMemoryValue := int64(math.Round(platformMemory)), int64(math.Round(businessMemory))
		snapshot.Platform.CPUUsageMilli = &platformCPUValue
		snapshot.Business.CPUUsageMilli = &businessCPUValue
		snapshot.Platform.MemoryUsage = &platformMemoryValue
		snapshot.Business.MemoryUsage = &businessMemoryValue
		snapshot.MetricsAvailable = true
		return
	}
}

func (connector *KubernetesConnector) WorkloadYAML(ctx context.Context, parsed ParsedKubeconfig, kind, namespace, name string) (WorkloadYAML, error) {
	clientset, err := connector.clientset(parsed)
	if err != nil {
		return WorkloadYAML{}, err
	}
	var object any
	switch strings.ToLower(kind) {
	case "deployment":
		value, getErr := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			err = getErr
		} else {
			value.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}
			value.ManagedFields = nil
			object = value
		}
	case "statefulset":
		value, getErr := clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			err = getErr
		} else {
			value.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"}
			value.ManagedFields = nil
			object = value
		}
	case "daemonset":
		value, getErr := clientset.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			err = getErr
		} else {
			value.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"}
			value.ManagedFields = nil
			object = value
		}
	default:
		return WorkloadYAML{}, newError(CodeWorkloadNotFound)
	}
	if apierrors.IsNotFound(err) {
		return WorkloadYAML{}, newError(CodeWorkloadNotFound)
	}
	if err != nil {
		return WorkloadYAML{}, mapConnectionError(err)
	}
	content, err := yaml.Marshal(object)
	if err != nil {
		return WorkloadYAML{}, newError(CodeConnectionFailed)
	}
	return WorkloadYAML{YAML: string(content)}, nil
}

type terminalSizeQueue struct{ sizes <-chan TerminalSize }

func (queue *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-queue.sizes
	if !ok {
		return nil
	}
	return &remotecommand.TerminalSize{Width: size.Width, Height: size.Height}
}

// podShellCommand starts an interactive shell with a stable prompt. The shell
// runs as the container process, so the prompt must be exported before exec
// rather than typed into the session.
func podShellCommand(shell, prompt string) []string {
	shellPath := "/bin/sh"
	if shell == "bash" {
		shellPath = "/bin/bash"
	}
	if prompt == "" {
		return []string{shellPath}
	}
	interactive := shellPath
	if shell == "bash" {
		// Avoid profile/rc files overriding PS1 after exec.
		interactive = "/bin/bash --noprofile --norc"
	}
	return []string{shellPath, "-c", "export PS1=" + shellSingleQuote(prompt) + "; exec " + interactive}
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func (connector *KubernetesConnector) ExecPod(ctx context.Context, parsed ParsedKubeconfig, namespace, name string, options PodExecOptions) error {
	config := connector.restConfig(parsed)
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return mapConnectionError(err)
	}
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return newError(CodePodNotFound)
	}
	if err != nil {
		return mapConnectionError(err)
	}
	if pod.Status.Phase != corev1.PodRunning {
		return newError(CodePodNotRunning)
	}
	found := false
	for _, container := range pod.Spec.Containers {
		if container.Name == options.Container {
			found = true
			break
		}
	}
	if !found {
		return newError(CodeContainerNotFound)
	}
	command := podShellCommand(options.Shell, options.Prompt)
	if len(options.Command) > 0 {
		command = options.Command
	}
	request := clientset.CoreV1().RESTClient().Post().Resource("pods").Namespace(namespace).Name(name).SubResource("exec").VersionedParams(&corev1.PodExecOptions{Container: options.Container, Command: command, Stdin: true, Stdout: true, Stderr: options.DisableTTY, TTY: !options.DisableTTY}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(config, http.MethodPost, request.URL())
	if err != nil {
		return mapConnectionError(err)
	}
	var sizeQueue remotecommand.TerminalSizeQueue
	if options.Sizes != nil {
		sizeQueue = &terminalSizeQueue{sizes: options.Sizes}
	}
	// Kubernetes combines stderr into stdout for TTY sessions. Passing a separate
	// stderr stream is invalid for some API server versions and proxies.
	streamOptions := remotecommand.StreamOptions{Stdin: options.Stdin, Stdout: options.Stdout, Tty: !options.DisableTTY, TerminalSizeQueue: sizeQueue}
	if options.DisableTTY {
		streamOptions.Stderr = options.Stderr
	}
	err = executor.StreamWithContext(ctx, streamOptions)
	if apierrors.IsForbidden(err) {
		return newError(CodePodExecForbidden)
	}
	if err != nil {
		return mapConnectionError(err)
	}
	return nil
}

const maxPodLogBytes int64 = 1024 * 1024

var (
	// ErrEventWatchExpired tells the collector to re-list immediately.
	ErrEventWatchExpired = errors.New("kubernetes event watch resource version expired")
	// ErrEventWatchClosed is returned when the server closes a healthy watch
	// stream (for example after TimeoutSeconds). The collector must back off
	// and re-list instead of restarting the watch in a tight loop.
	ErrEventWatchClosed = errors.New("kubernetes event watch closed")
)

func (connector *KubernetesConnector) ListEvents(ctx context.Context, parsed ParsedKubeconfig) (EventBatch, error) {
	client, err := connector.listClientset(parsed)
	if err != nil {
		return EventBatch{}, err
	}
	batch := EventBatch{APIVersion: "events.k8s.io/v1", Items: []ClusterEvent{}}
	missing := false
	err = visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		page, e := client.EventsV1().Events("").List(ctx, options)
		if e != nil {
			missing = apierrors.IsNotFound(e) && options.Continue == ""
			return nil, e
		}
		batch.ResourceVersion = page.ResourceVersion
		return page, nil
	}, func(item *eventsv1.Event) error {
		batch.Items = append(batch.Items, normalizedEventV1(item))
		return nil
	})
	if err == nil {
		return batch, nil
	}
	if !missing {
		return EventBatch{}, err
	}
	batch = EventBatch{APIVersion: "v1", Items: []ClusterEvent{}}
	err = visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		page, e := client.CoreV1().Events("").List(ctx, options)
		if e != nil {
			return nil, e
		}
		batch.ResourceVersion = page.ResourceVersion
		return page, nil
	}, func(item *corev1.Event) error {
		batch.Items = append(batch.Items, normalizedCoreEvent(item))
		return nil
	})
	if err != nil {
		return EventBatch{}, err
	}
	return batch, nil
}

func (connector *KubernetesConnector) WatchEvents(ctx context.Context, parsed ParsedKubeconfig, apiVersion string, resourceVersion string, receive func(ClusterEvent) error) error {
	// A watch is a long-lived response body. rest.Config.Timeout is applied as
	// http.Client.Timeout, which covers the whole response including the body,
	// so it would cut every watch after the general request timeout. Watches
	// are bounded by TimeoutSeconds and the caller's context instead.
	config := connector.watchConfig(parsed)
	httpClient, err := connector.httpClient(config)
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfigAndClient(config, httpClient)
	if err != nil {
		return mapConnectionError(err)
	}
	timeout := int64(300)
	options := metav1.ListOptions{AllowWatchBookmarks: true, ResourceVersion: resourceVersion, TimeoutSeconds: &timeout}
	var watcher watch.Interface
	if apiVersion == "events.k8s.io/v1" {
		watcher, err = clientset.EventsV1().Events("").Watch(ctx, options)
	} else {
		watcher, err = clientset.CoreV1().Events("").Watch(ctx, options)
	}
	if err != nil {
		if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) {
			return ErrEventWatchExpired
		}
		return mapConnectionError(err)
	}
	defer watcher.Stop()
	for item := range watcher.ResultChan() {
		if item.Type == watch.Error {
			statusError := apierrors.FromObject(item.Object)
			if apierrors.IsResourceExpired(statusError) || apierrors.IsGone(statusError) {
				return ErrEventWatchExpired
			}
			return mapConnectionError(statusError)
		}
		if item.Type != watch.Added && item.Type != watch.Modified {
			continue
		}
		var event ClusterEvent
		switch value := item.Object.(type) {
		case *eventsv1.Event:
			event = normalizedEventV1(value)
		case *corev1.Event:
			event = normalizedCoreEvent(value)
		default:
			continue
		}
		if err := receive(event); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrEventWatchClosed
}

func (connector *KubernetesConnector) clientset(parsed ParsedKubeconfig) (*kubernetes.Clientset, error) {
	return connector.clientsetWithTimeout(parsed, connector.timeout)
}

func (connector *KubernetesConnector) clientsetWithTimeout(parsed ParsedKubeconfig, timeout time.Duration) (*kubernetes.Clientset, error) {
	config := connector.restConfig(parsed)
	config.Timeout = timeout
	httpClient, err := connector.httpClient(config)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfigAndClient(config, httpClient)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return clientset, nil
}

func (connector *KubernetesConnector) listClientset(parsed ParsedKubeconfig) (*kubernetes.Clientset, error) {
	config := connector.restConfig(parsed)
	config.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
	httpClient, err := connector.httpClient(config)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfigAndClient(config, httpClient)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	return clientset, nil
}

func (connector *KubernetesConnector) watchConfig(parsed ParsedKubeconfig) *rest.Config {
	config := connector.restConfig(parsed)
	config.Timeout = 0
	return config
}

func normalizedEventV1(event *eventsv1.Event) ClusterEvent {
	first := event.EventTime.Time
	last := first
	count := int64(1)
	if event.Series != nil {
		count = int64(event.Series.Count)
		last = event.Series.LastObservedTime.Time
	}
	if event.DeprecatedFirstTimestamp.Time.After(time.Time{}) {
		first = event.DeprecatedFirstTimestamp.Time
	}
	if event.DeprecatedLastTimestamp.Time.After(time.Time{}) {
		last = event.DeprecatedLastTimestamp.Time
	}
	if event.DeprecatedCount > 0 {
		count = int64(event.DeprecatedCount)
	}
	return normalizedEvent(event.Namespace, string(event.UID), event.Type, event.Reason, event.Action, event.Note, event.Regarding, event.ReportingController, event.ReportingInstance, count, first, last, event.ResourceVersion, event.CreationTimestamp.Time)
}

func normalizedCoreEvent(event *corev1.Event) ClusterEvent {
	first := event.FirstTimestamp.Time
	last := event.LastTimestamp.Time
	if event.EventTime.Time.After(time.Time{}) {
		last = event.EventTime.Time
		if first.IsZero() {
			first = last
		}
	}
	count := int64(event.Count)
	if event.Series != nil {
		count = int64(event.Series.Count)
		last = event.Series.LastObservedTime.Time
	}
	controller := event.ReportingController
	instance := event.ReportingInstance
	if controller == "" {
		controller = event.Source.Component
		instance = event.Source.Host
	}
	return normalizedEvent(event.Namespace, string(event.UID), event.Type, event.Reason, event.Action, event.Message, event.InvolvedObject, controller, instance, count, first, last, event.ResourceVersion, event.CreationTimestamp.Time)
}

func normalizedEvent(namespace string, uid string, eventType string, reason string, action string, message string, regarding corev1.ObjectReference, controller string, instance string, count int64, first time.Time, last time.Time, resourceVersion string, created time.Time) ClusterEvent {
	if len(message) > 4096 {
		end := 4096
		for end > 0 && !utf8.RuneStart(message[end]) {
			end--
		}
		message = strings.Clone(message[:end]) + "… [truncated]"
	}
	if first.IsZero() {
		first = created
	}
	if last.IsZero() {
		last = first
	}
	if count < 1 {
		count = 1
	}
	return ClusterEvent{
		EventUID: uid, Namespace: namespace, Type: eventType, Reason: reason, Action: action, Message: message,
		RegardingAPIVersion: regarding.APIVersion, RegardingKind: regarding.Kind, RegardingNamespace: regarding.Namespace,
		RegardingName: regarding.Name, RegardingUID: string(regarding.UID), ReportingController: controller,
		ReportingInstance: instance, OccurrenceCount: count, FirstObservedAt: first.UTC(), LastObservedAt: last.UTC(),
		ResourceVersion: resourceVersion,
	}
}

func (connector *KubernetesConnector) PodLogs(ctx context.Context, parsed ParsedKubeconfig, namespace string, name string, requestOptions PodLogOptions) (PodLogSnapshot, error) {
	config := connector.restConfig(parsed)
	httpClient, err := connector.httpClient(config)
	if err != nil {
		return PodLogSnapshot{}, err
	}
	clientset, err := kubernetes.NewForConfigAndClient(config, httpClient)
	if err != nil {
		return PodLogSnapshot{}, mapConnectionError(err)
	}
	options := &corev1.PodLogOptions{
		Container:  requestOptions.Container,
		LimitBytes: pointer(maxPodLogBytes),
		Previous:   requestOptions.Previous,
		Timestamps: true,
	}
	if requestOptions.SinceTime != nil {
		sinceTime := metav1.NewTime(*requestOptions.SinceTime)
		options.SinceTime = &sinceTime
	} else {
		options.TailLines = pointer(requestOptions.TailLines)
	}
	content, err := clientset.CoreV1().Pods(namespace).GetLogs(name, options).DoRaw(ctx)
	if apierrors.IsNotFound(err) {
		return PodLogSnapshot{}, newError(CodePodNotFound)
	}
	if err != nil {
		return PodLogSnapshot{}, mapConnectionError(err)
	}
	text := string(content)
	if requestOptions.UntilTime != nil {
		text = logsUntil(text, *requestOptions.UntilTime)
	}
	return PodLogSnapshot{
		Container: requestOptions.Container, Text: text, TailLines: requestOptions.TailLines, Previous: requestOptions.Previous,
		Truncated: int64(len(content)) >= maxPodLogBytes,
		SinceTime: requestOptions.SinceTime, UntilTime: requestOptions.UntilTime,
	}, nil
}

func logsUntil(content string, until time.Time) string {
	lines := strings.SplitAfter(content, "\n")
	var result strings.Builder
	for _, line := range lines {
		if line == "" {
			continue
		}
		timestampText, _, _ := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
		timestamp, err := time.Parse(time.RFC3339Nano, timestampText)
		if err != nil || !timestamp.After(until) {
			result.WriteString(line)
		}
	}
	return result.String()
}

func pointer[T any](value T) *T {
	return &value
}

func (connector *KubernetesConnector) Pod(ctx context.Context, parsed ParsedKubeconfig, namespace string, name string) (ClusterPodDetail, error) {
	config := connector.restConfig(parsed)
	httpClient, err := connector.httpClient(config)
	if err != nil {
		return ClusterPodDetail{}, err
	}
	clientset, err := kubernetes.NewForConfigAndClient(config, httpClient)
	if err != nil {
		return ClusterPodDetail{}, mapConnectionError(err)
	}
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return ClusterPodDetail{}, newError(CodePodNotFound)
	}
	if err != nil {
		return ClusterPodDetail{}, mapConnectionError(err)
	}
	replicaSets, err := clientset.AppsV1().ReplicaSets(namespace).List(ctx, cachedListOptions)
	if err != nil {
		return ClusterPodDetail{}, mapConnectionError(err)
	}
	detail := ClusterPodDetail{
		ClusterPod:     clusterPod(*pod, replicaSetOwnerMap(replicaSets.Items)),
		Labels:         copyLabels(pod.Labels),
		ServiceAccount: pod.Spec.ServiceAccountName,
		HostIP:         pod.Status.HostIP,
		QoSClass:       string(pod.Status.QOSClass),
		Containers:     podContainers(*pod),
		Conditions:     podConditions(*pod),
	}
	return detail, nil
}

func (connector *KubernetesConnector) Pods(ctx context.Context, parsed ParsedKubeconfig) ([]ClusterPod, error) {
	clientset, err := connector.listClientset(parsed)
	if err != nil {
		return nil, err
	}
	replicaSetOwners := map[string]metav1.OwnerReference{}
	if err := visitReplicaSets(ctx, clientset, "", func(item *appsv1.ReplicaSet) error {
		if owner := controllerOwnerReference(item.OwnerReferences); owner != nil {
			replicaSetOwners[string(item.UID)] = *owner
		}
		return nil
	}); err != nil {
		return nil, err
	}
	result := []ClusterPod{}
	if err := visitPods(ctx, clientset, "", func(item *corev1.Pod) error { result = append(result, clusterPod(*item, replicaSetOwners)); return nil }); err != nil {
		return nil, err
	}

	sort.Slice(result, func(left, right int) bool {
		if result[left].Namespace != result[right].Namespace {
			return result[left].Namespace < result[right].Namespace
		}
		return result[left].Name < result[right].Name
	})
	return result, nil
}

func replicaSetOwnerMap(replicaSets []appsv1.ReplicaSet) map[string]metav1.OwnerReference {
	owners := make(map[string]metav1.OwnerReference, len(replicaSets))
	for _, replicaSet := range replicaSets {
		if owner := controllerOwnerReference(replicaSet.OwnerReferences); owner != nil {
			owners[string(replicaSet.UID)] = *owner
		}
	}
	return owners
}

func clusterPod(pod corev1.Pod, replicaSetOwners map[string]metav1.OwnerReference) ClusterPod {
	summary := summarizeWorkloadPod(pod)
	owner := controllerOwnerReference(pod.OwnerReferences)
	if owner != nil && owner.Kind == "ReplicaSet" {
		if controller, ok := replicaSetOwners[string(owner.UID)]; ok {
			owner = &controller
		}
	}
	result := ClusterPod{
		Name: pod.Name, Namespace: pod.Namespace, Phase: summary.Phase, StatusReason: summary.StatusReason,
		ReadyContainers: summary.ReadyContainers, TotalContainers: summary.TotalContainers,
		RestartCount: summary.RestartCount, NodeName: summary.NodeName, PodIP: summary.PodIP,
		Images: podTemplateImages(pod.Spec), CreatedAt: summary.CreatedAt, StartedAt: summary.StartedAt,
	}
	if owner != nil {
		result.OwnerKind = owner.Kind
		result.OwnerName = owner.Name
	}
	return result
}

func podContainers(pod corev1.Pod) []PodContainer {
	statuses := make(map[string]corev1.ContainerStatus, len(pod.Status.ContainerStatuses))
	for _, status := range pod.Status.ContainerStatuses {
		statuses[status.Name] = status
	}
	containers := make([]PodContainer, 0, len(pod.Spec.Containers))
	for _, container := range pod.Spec.Containers {
		item := PodContainer{Name: container.Name, Image: container.Image, State: "Waiting"}
		if status, ok := statuses[container.Name]; ok {
			item.Ready = status.Ready
			item.RestartCount = status.RestartCount
			switch {
			case status.State.Running != nil:
				item.State = "Running"
			case status.State.Terminated != nil:
				item.State = "Terminated"
				item.StatusReason = status.State.Terminated.Reason
			case status.State.Waiting != nil:
				item.State = "Waiting"
				item.StatusReason = status.State.Waiting.Reason
			}
		}
		containers = append(containers, item)
	}
	return containers
}

func podConditions(pod corev1.Pod) []PodCondition {
	conditions := make([]PodCondition, 0, len(pod.Status.Conditions))
	for _, condition := range pod.Status.Conditions {
		transition := condition.LastTransitionTime.Time
		conditions = append(conditions, PodCondition{
			Type: string(condition.Type), Status: string(condition.Status), Reason: condition.Reason,
			Message: condition.Message, LastTransitionTime: &transition,
		})
	}
	return conditions
}

type HealthSnapshot struct {
	NodesReady          int
	NodesTotal          int
	InstancesReady      int
	InstancesTotal      int
	CoreComponentsReady int
	CoreComponentsTotal int
	WorkerNodes         int
	WorkerCores         int64
	APILatencyMS        int64
	CPUUtilization      *float64
	MemoryUtilization   *float64
	MetricsAvailable    bool
}

type prometheusQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Value []json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

type prometheusService struct {
	namespace string
	proxyName string
	score     int
}

type KubernetesConnector struct {
	timeout                   time.Duration
	healthInstanceSource      string
	securityScannerNamespace  string
	securityScannerPullSecret string
	securityPodStartupTimeout time.Duration
	kubeBenchImage            string
	kubeBenchImageAMD64       string
	kubeBenchImageARM64       string
	hostScanConcurrency       int
}

func NewKubernetesConnector(timeout time.Duration) *KubernetesConnector {
	return NewKubernetesConnectorWithInstanceSource(timeout, "pods")
}

// NewKubernetesConnectorWithInstanceSource selects how the health snapshot
// counts "instances". "workloads" derives the counters from Deployment,
// StatefulSet and DaemonSet status and only lists kube-system Pods, avoiding a
// full Pod list on every health round.
func NewKubernetesConnectorWithInstanceSource(timeout time.Duration, source string) *KubernetesConnector {
	if source != "workloads" {
		source = "pods"
	}
	return &KubernetesConnector{timeout: timeout, healthInstanceSource: source, securityScannerNamespace: "kube-system", securityPodStartupTimeout: defaultSecurityPodStartupTimeout, kubeBenchImage: defaultKubeBenchImage, hostScanConcurrency: defaultHostScanConcurrency}
}

func (connector *KubernetesConnector) Version(ctx context.Context, parsed ParsedKubeconfig) (string, error) {
	config := connector.restConfig(parsed)
	httpClient, err := connector.httpClient(config)
	if err != nil {
		return "", err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfigAndClient(config, httpClient)
	if err != nil {
		return "", mapConnectionError(err)
	}
	version, err := discoveryClient.ServerVersion()
	if err != nil {
		return "", mapConnectionError(err)
	}
	if version == nil || version.GitVersion == "" {
		return "", newError(CodeConnectionFailed)
	}
	return version.GitVersion, nil
}

func (connector *KubernetesConnector) Health(ctx context.Context, parsed ParsedKubeconfig) (HealthSnapshot, error) {
	clientset, err := connector.listClientset(parsed)
	if err != nil {
		return HealthSnapshot{}, err
	}

	startedAt := time.Now()
	snapshot := HealthSnapshot{}
	var workerCPUMilli int64
	var totalCPUMilli, totalMemoryBytes int64
	if err := visitNodes(ctx, clientset, func(item *corev1.Node) error {
		node := *item
		snapshot.NodesTotal++

		totalCPUMilli += node.Status.Capacity.Cpu().MilliValue()
		totalMemoryBytes += node.Status.Capacity.Memory().Value()
		if nodeReady(node) {
			snapshot.NodesReady++
		}
		if isWorkerNode(node) {
			snapshot.WorkerNodes++
			workerCPUMilli += node.Status.Capacity.Cpu().MilliValue()
		}
		return nil
	}); err != nil {
		return HealthSnapshot{}, err
	}
	snapshot.WorkerCores = workerCPUMilli / 1000
	snapshot.APILatencyMS = time.Since(startedAt).Milliseconds()

	if connector.healthInstanceSource == "workloads" {
		if err := connector.collectWorkloadHealth(ctx, clientset, &snapshot); err != nil {
			return HealthSnapshot{}, err
		}
	} else {
		if err := visitPods(ctx, clientset, "", func(item *corev1.Pod) error {
			pod := *item

			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				return nil
			}
			snapshot.InstancesTotal++
			if podReady(pod) {
				snapshot.InstancesReady++
			}
			if pod.Namespace == "kube-system" {
				snapshot.CoreComponentsTotal++
				if podReady(pod) {
					snapshot.CoreComponentsReady++
				}
			}
			return nil
		}); err != nil {
			return HealthSnapshot{}, err
		}
	}
	services, err := boundedServices(ctx, clientset)
	if err == nil {
		connector.collectPrometheusMetrics(ctx, clientset, services.Items, totalCPUMilli, totalMemoryBytes, &snapshot)
	}
	return snapshot, nil
}

func (connector *KubernetesConnector) collectWorkloadHealth(ctx context.Context, clientset *kubernetes.Clientset, snapshot *HealthSnapshot) error {
	if err := visitDeployments(ctx, clientset, "", func(item *appsv1.Deployment) error {
		snapshot.InstancesTotal += int(desiredReplicas(item.Spec.Replicas))
		snapshot.InstancesReady += int(item.Status.ReadyReplicas)
		return nil
	}); err != nil {
		return err
	}
	if err := visitStatefulSets(ctx, clientset, "", func(item *appsv1.StatefulSet) error {
		snapshot.InstancesTotal += int(desiredReplicas(item.Spec.Replicas))
		snapshot.InstancesReady += int(item.Status.ReadyReplicas)
		return nil
	}); err != nil {
		return err
	}
	if err := visitDaemonSets(ctx, clientset, "", func(item *appsv1.DaemonSet) error {
		snapshot.InstancesTotal += int(item.Status.DesiredNumberScheduled)
		snapshot.InstancesReady += int(item.Status.NumberReady)
		return nil
	}); err != nil {
		return err
	}
	return visitPods(ctx, clientset, "kube-system", func(item *corev1.Pod) error {
		if item.Status.Phase == corev1.PodSucceeded || item.Status.Phase == corev1.PodFailed {
			return nil
		}
		snapshot.CoreComponentsTotal++
		if podReady(*item) {
			snapshot.CoreComponentsReady++
		}
		return nil
	})
}

func (connector *KubernetesConnector) Nodes(ctx context.Context, parsed ParsedKubeconfig) ([]ClusterNode, error) {
	clientset, err := connector.listClientset(parsed)
	if err != nil {
		return nil, err
	}

	nodes := []ClusterNode{}
	if err := visitNodes(ctx, clientset, func(item *corev1.Node) error {
		node := *item
		nodes = append(nodes, ClusterNode{
			Name:              node.Name,
			Status:            nodeStatus(node),
			Roles:             nodeRoles(node),
			Region:            firstNodeLabel(node, "topology.kubernetes.io/region", "failure-domain.beta.kubernetes.io/region"),
			Zone:              firstNodeLabel(node, "topology.kubernetes.io/zone", "failure-domain.beta.kubernetes.io/zone"),
			InstanceType:      firstNodeLabel(node, "node.kubernetes.io/instance-type", "beta.kubernetes.io/instance-type"),
			CPUCores:          float64(node.Status.Capacity.Cpu().MilliValue()) / 1000,
			MemoryBytes:       node.Status.Capacity.Memory().Value(),
			KubernetesVersion: node.Status.NodeInfo.KubeletVersion,
			Schedulable:       !node.Spec.Unschedulable,
			CreatedAt:         node.CreationTimestamp.Time,
		})
		return nil
	}); err != nil {
		return nil, err
	}

	sort.Slice(nodes, func(left, right int) bool { return nodes[left].Name < nodes[right].Name })
	return nodes, nil
}

func (connector *KubernetesConnector) Workloads(ctx context.Context, parsed ParsedKubeconfig) ([]ClusterWorkload, error) {
	details, err := connector.collectWorkloads(ctx, parsed)
	if err != nil {
		return nil, err
	}
	workloads := make([]ClusterWorkload, len(details))
	for index := range details {
		workloads[index] = details[index].ClusterWorkload
	}
	return workloads, nil
}

func (connector *KubernetesConnector) Workload(ctx context.Context, parsed ParsedKubeconfig, kind string, namespace string, name string) (ClusterWorkloadDetail, error) {
	details, err := connector.collectWorkloads(ctx, parsed)
	if err != nil {
		return ClusterWorkloadDetail{}, err
	}
	for _, detail := range details {
		if strings.EqualFold(detail.Kind, kind) && detail.Namespace == namespace && detail.Name == name {
			return detail, nil
		}
	}
	return ClusterWorkloadDetail{}, newError(CodeWorkloadNotFound)
}

func (connector *KubernetesConnector) collectWorkloads(ctx context.Context, parsed ParsedKubeconfig) ([]ClusterWorkloadDetail, error) {
	clientset, err := connector.listClientset(parsed)
	if err != nil {
		return nil, err
	}

	details := []ClusterWorkloadDetail{}
	workloadByUID := map[string]int{}
	if err := visitDeployments(ctx, clientset, "", func(item *appsv1.Deployment) error {
		workloadByUID[string(item.UID)] = len(details)
		details = append(details, deploymentWorkload(*item))
		return nil
	}); err != nil {
		return nil, err
	}
	if err := visitStatefulSets(ctx, clientset, "", func(item *appsv1.StatefulSet) error {
		workloadByUID[string(item.UID)] = len(details)
		details = append(details, statefulSetWorkload(*item))
		return nil
	}); err != nil {
		return nil, err
	}
	if err := visitDaemonSets(ctx, clientset, "", func(item *appsv1.DaemonSet) error {
		workloadByUID[string(item.UID)] = len(details)
		details = append(details, daemonSetWorkload(*item))
		return nil
	}); err != nil {
		return nil, err
	}
	replicaSetOwners := map[string]string{}
	if err := visitReplicaSets(ctx, clientset, "", func(item *appsv1.ReplicaSet) error {
		if owner := controllerOwnerUID(item.OwnerReferences); owner != "" {
			replicaSetOwners[string(item.UID)] = owner
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if err := visitPods(ctx, clientset, "", func(item *corev1.Pod) error {
		pod := *item
		owner := controllerOwnerUID(pod.OwnerReferences)
		if owner == "" {
			return nil
		}
		if deploymentOwner := replicaSetOwners[owner]; deploymentOwner != "" {
			owner = deploymentOwner
		}
		index, ok := workloadByUID[owner]
		if !ok {
			return nil
		}
		podSummary := summarizeWorkloadPod(pod)
		details[index].Pods = append(details[index].Pods, podSummary)
		details[index].PodsTotal++
		details[index].RestartCount += podSummary.RestartCount
		if podReady(pod) {
			details[index].PodsReady++
		}
		return nil
	}); err != nil {
		return nil, err
	}

	for index := range details {
		sort.Slice(details[index].Pods, func(left, right int) bool {
			return details[index].Pods[left].Name < details[index].Pods[right].Name
		})
	}
	sort.Slice(details, func(left, right int) bool {
		if details[left].Namespace != details[right].Namespace {
			return details[left].Namespace < details[right].Namespace
		}
		if details[left].Kind != details[right].Kind {
			return details[left].Kind < details[right].Kind
		}
		return details[left].Name < details[right].Name
	})
	return details, nil
}

func deploymentWorkload(item appsv1.Deployment) ClusterWorkloadDetail {
	status := "healthy"
	reason := "副本均已就绪"
	for _, condition := range item.Status.Conditions {
		if condition.Type == appsv1.DeploymentProgressing && condition.Status == corev1.ConditionFalse && condition.Reason == "ProgressDeadlineExceeded" {
			status = "degraded"
			reason = "发布超过进度期限"
		}
	}
	if status != "degraded" && (item.Status.ObservedGeneration < item.Generation || item.Status.ReadyReplicas < desiredReplicas(item.Spec.Replicas) || item.Status.UpdatedReplicas < desiredReplicas(item.Spec.Replicas)) {
		status = "progressing"
		reason = "正在发布或等待实例就绪"
	}
	return ClusterWorkloadDetail{
		ClusterWorkload: ClusterWorkload{
			Kind: "Deployment", Namespace: item.Namespace, Name: item.Name, Status: status, StatusReason: reason,
			Desired: desiredReplicas(item.Spec.Replicas), Ready: item.Status.ReadyReplicas, Available: item.Status.AvailableReplicas,
			Updated: item.Status.UpdatedReplicas, Images: podTemplateImages(item.Spec.Template.Spec), CreatedAt: item.CreationTimestamp.Time,
			LastUpdatedAt: latestDeploymentTransition(item),
		},
		Labels: copyLabels(item.Labels), Pods: []WorkloadPod{},
	}
}

func statefulSetWorkload(item appsv1.StatefulSet) ClusterWorkloadDetail {
	desired := desiredReplicas(item.Spec.Replicas)
	status := "healthy"
	reason := "副本均已就绪"
	if item.Status.ObservedGeneration < item.Generation || item.Status.ReadyReplicas < desired || item.Status.CurrentRevision != item.Status.UpdateRevision {
		status = "progressing"
		reason = "正在更新或等待有状态实例就绪"
	}
	return ClusterWorkloadDetail{
		ClusterWorkload: ClusterWorkload{
			Kind: "StatefulSet", Namespace: item.Namespace, Name: item.Name, Status: status, StatusReason: reason,
			Desired: desired, Ready: item.Status.ReadyReplicas, Available: item.Status.AvailableReplicas,
			Updated: item.Status.UpdatedReplicas, Images: podTemplateImages(item.Spec.Template.Spec), CreatedAt: item.CreationTimestamp.Time,
			LastUpdatedAt: item.CreationTimestamp.Time,
		},
		Labels: copyLabels(item.Labels), Pods: []WorkloadPod{},
	}
}

func daemonSetWorkload(item appsv1.DaemonSet) ClusterWorkloadDetail {
	status := "healthy"
	reason := "目标节点实例均已就绪"
	if item.Status.ObservedGeneration < item.Generation || item.Status.UpdatedNumberScheduled < item.Status.DesiredNumberScheduled {
		status = "progressing"
		reason = "正在更新节点实例"
	} else if item.Status.NumberUnavailable > 0 || item.Status.NumberMisscheduled > 0 {
		status = "degraded"
		reason = "存在不可用或调度位置异常的实例"
	}
	return ClusterWorkloadDetail{
		ClusterWorkload: ClusterWorkload{
			Kind: "DaemonSet", Namespace: item.Namespace, Name: item.Name, Status: status, StatusReason: reason,
			Desired: item.Status.DesiredNumberScheduled, Ready: item.Status.NumberReady, Available: item.Status.NumberAvailable,
			Updated: item.Status.UpdatedNumberScheduled, Images: podTemplateImages(item.Spec.Template.Spec), CreatedAt: item.CreationTimestamp.Time,
			LastUpdatedAt: item.CreationTimestamp.Time,
		},
		Labels: copyLabels(item.Labels), Pods: []WorkloadPod{},
	}
}

func summarizeWorkloadPod(pod corev1.Pod) WorkloadPod {
	summary := WorkloadPod{
		Name: pod.Name, Phase: string(pod.Status.Phase), StatusReason: pod.Status.Reason,
		TotalContainers: len(pod.Spec.Containers), NodeName: pod.Spec.NodeName, PodIP: pod.Status.PodIP,
		CreatedAt: pod.CreationTimestamp.Time,
	}
	if pod.Status.StartTime != nil {
		startedAt := pod.Status.StartTime.Time
		summary.StartedAt = &startedAt
	}
	for _, container := range pod.Status.ContainerStatuses {
		summary.RestartCount += container.RestartCount
		if container.Ready {
			summary.ReadyContainers++
		}
		if summary.StatusReason == "" && container.State.Waiting != nil && container.State.Waiting.Reason != "" {
			summary.StatusReason = container.State.Waiting.Reason
		}
		if summary.StatusReason == "" && container.State.Terminated != nil && container.State.Terminated.Reason != "" {
			summary.StatusReason = container.State.Terminated.Reason
		}
	}
	if summary.StatusReason == "" {
		summary.StatusReason = summary.Phase
	}
	return summary
}

func controllerOwnerUID(owners []metav1.OwnerReference) string {
	owner := controllerOwnerReference(owners)
	if owner == nil {
		return ""
	}
	return string(owner.UID)
}

func controllerOwnerReference(owners []metav1.OwnerReference) *metav1.OwnerReference {
	for index := range owners {
		if owners[index].Controller != nil && *owners[index].Controller {
			return &owners[index]
		}
	}
	return nil
}

func desiredReplicas(value *int32) int32 {
	if value == nil {
		return 1
	}
	return *value
}

func podTemplateImages(spec corev1.PodSpec) []string {
	images := make([]string, 0, len(spec.InitContainers)+len(spec.Containers))
	seen := make(map[string]struct{})
	for _, container := range append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...) {
		if container.Image == "" {
			continue
		}
		if _, ok := seen[container.Image]; ok {
			continue
		}
		seen[container.Image] = struct{}{}
		images = append(images, container.Image)
	}
	return images
}

func latestDeploymentTransition(item appsv1.Deployment) time.Time {
	latest := item.CreationTimestamp.Time
	for _, condition := range item.Status.Conditions {
		if condition.LastUpdateTime.Time.After(latest) {
			latest = condition.LastUpdateTime.Time
		}
	}
	return latest
}

func copyLabels(labels map[string]string) map[string]string {
	copied := make(map[string]string, len(labels))
	for key, value := range labels {
		copied[key] = value
	}
	return copied
}

func (connector *KubernetesConnector) collectPrometheusMetrics(
	ctx context.Context,
	clientset *kubernetes.Clientset,
	services []corev1.Service,
	totalCPUMilli int64,
	totalMemoryBytes int64,
	snapshot *HealthSnapshot,
) {
	candidates := discoverPrometheusServices(services)
	const cpuQuery = `sum(rate(container_cpu_usage_seconds_total{container!="",container!="POD"}[5m]))`
	const memoryQuery = `sum(container_memory_working_set_bytes{container!="",container!="POD"})`
	for _, candidate := range candidates {
		usedCPUCores, cpuOK := queryPrometheus(ctx, clientset, candidate, cpuQuery)
		usedMemoryBytes, memoryOK := queryPrometheus(ctx, clientset, candidate, memoryQuery)
		if !cpuOK || !memoryOK || totalCPUMilli <= 0 || totalMemoryBytes <= 0 {
			continue
		}
		cpu := math.Max(0, math.Min(100, usedCPUCores/(float64(totalCPUMilli)/1000)*100))
		memory := math.Max(0, math.Min(100, usedMemoryBytes/float64(totalMemoryBytes)*100))
		snapshot.CPUUtilization = &cpu
		snapshot.MemoryUtilization = &memory
		snapshot.MetricsAvailable = true
		return
	}
}

func discoverPrometheusServices(services []corev1.Service) []prometheusService {
	candidates := make([]prometheusService, 0)
	for _, service := range services {
		identity := strings.ToLower(service.Namespace + "/" + service.Name + "/" + service.Labels["app"] + "/" + service.Labels["app.kubernetes.io/name"])
		if !strings.Contains(identity, "prometheus") {
			continue
		}
		for _, port := range service.Spec.Ports {
			portName := strings.ToLower(port.Name)
			if port.Port != 9090 && !strings.Contains(portName, "web") && !strings.Contains(portName, "http") {
				continue
			}
			scheme := "http"
			if port.Port == 443 || strings.Contains(portName, "https") {
				scheme = "https"
			}
			portReference := port.Name
			if portReference == "" {
				portReference = strconv.Itoa(int(port.Port))
			}
			score := 0
			if strings.Contains(strings.ToLower(service.Name), "prometheus") {
				score += 10
			}
			if strings.Contains(strings.ToLower(service.Namespace), "monitoring") {
				score += 5
			}
			if port.Port == 9090 {
				score += 3
			}
			candidates = append(candidates, prometheusService{
				namespace: service.Namespace,
				proxyName: fmt.Sprintf("%s:%s:%s", scheme, service.Name, portReference),
				score:     score,
			})
		}
	}
	sort.SliceStable(candidates, func(left, right int) bool { return candidates[left].score > candidates[right].score })
	return candidates
}

// queryPrometheusSum evaluates an aggregation query where an empty result set
// is a meaningful zero: the sum over an empty set is 0, not missing data.
// A request failure still reports the data source as unavailable, so a broken
// Prometheus is never silently reported as zero usage.
func queryPrometheusSum(ctx context.Context, clientset *kubernetes.Clientset, service prometheusService, query string) (float64, bool) {
	raw, err := clientset.CoreV1().RESTClient().Get().
		Namespace(service.namespace).
		Resource("services").
		Name(service.proxyName).
		SubResource("proxy").
		Suffix("api", "v1", "query").
		Param("query", query).
		Do(ctx).
		Raw()
	if err != nil {
		return 0, false
	}
	var response prometheusQueryResponse
	if err := json.Unmarshal(raw, &response); err != nil || response.Status != "success" {
		return 0, false
	}
	if len(response.Data.Result) == 0 {
		return 0, true
	}
	value, ok := prometheusSampleValue(response)
	if !ok {
		return 0, false
	}
	return value, true
}

func queryPrometheus(ctx context.Context, clientset *kubernetes.Clientset, service prometheusService, query string) (float64, bool) {
	raw, err := clientset.CoreV1().RESTClient().Get().
		Namespace(service.namespace).
		Resource("services").
		Name(service.proxyName).
		SubResource("proxy").
		Suffix("api", "v1", "query").
		Param("query", query).
		Do(ctx).
		Raw()
	if err != nil {
		return 0, false
	}
	var response prometheusQueryResponse
	if err := json.Unmarshal(raw, &response); err != nil || response.Status != "success" || len(response.Data.Result) == 0 {
		return 0, false
	}
	return prometheusSampleValue(response)
}

// prometheusSampleValue extracts the first sample of a scalar/vector response.
func prometheusSampleValue(response prometheusQueryResponse) (float64, bool) {
	if len(response.Data.Result) == 0 || len(response.Data.Result[0].Value) < 2 {
		return 0, false
	}
	var valueText string
	if err := json.Unmarshal(response.Data.Result[0].Value[1], &valueText); err != nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(valueText, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func (connector *KubernetesConnector) restConfig(parsed ParsedKubeconfig) *rest.Config {
	return &rest.Config{
		Host:        parsed.APIServer,
		BearerToken: parsed.Token,
		Timeout:     connector.timeout,
		TLSClientConfig: rest.TLSClientConfig{
			CAData:   parsed.CAData,
			CertData: parsed.ClientCertificateData,
			KeyData:  parsed.ClientKeyData,
		},
	}
}

func (connector *KubernetesConnector) httpClient(config *rest.Config) (*http.Client, error) {
	httpClient, err := rest.HTTPClientFor(config)
	if err != nil {
		return nil, mapConnectionError(err)
	}
	copyClient := *httpClient
	httpClient = &copyClient
	baseTransport := httpClient.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	httpClient.Transport = listBudgetTransport{base: baseTransport}
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return httpClient, nil
}

func nodeReady(node corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func isWorkerNode(node corev1.Node) bool {
	for label := range node.Labels {
		if label == "node-role.kubernetes.io/control-plane" || label == "node-role.kubernetes.io/master" {
			return false
		}
	}
	return true
}

func nodeStatus(node corev1.Node) string {
	for _, condition := range node.Status.Conditions {
		if condition.Type != corev1.NodeReady {
			continue
		}
		switch condition.Status {
		case corev1.ConditionTrue:
			return "Ready"
		case corev1.ConditionFalse:
			return "NotReady"
		default:
			return "Unknown"
		}
	}
	return "Unknown"
}

func nodeRoles(node corev1.Node) []string {
	const prefix = "node-role.kubernetes.io/"
	roles := make([]string, 0)
	for label := range node.Labels {
		if strings.HasPrefix(label, prefix) {
			role := strings.TrimPrefix(label, prefix)
			if role != "" {
				roles = append(roles, role)
			}
		}
	}
	if len(roles) == 0 {
		roles = append(roles, "worker")
	}
	sort.Strings(roles)
	return roles
}

func firstNodeLabel(node corev1.Node, labels ...string) string {
	for _, label := range labels {
		if value := node.Labels[label]; value != "" {
			return value
		}
	}
	return ""
}

func mapConnectionError(err error) error {
	if errors.Is(err, errListBudget) {
		return newError(CodeCollectionLimitExceeded)
	}
	var known *Error
	if errors.As(err, &known) {
		return known
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return newError(CodeConnectionTimeout)
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		return newError(CodeConnectionTimeout)
	}
	if apierrors.IsUnauthorized(err) {
		return newError(CodeAuthenticationFailed)
	}
	if apierrors.IsForbidden(err) {
		return newError(CodeAuthorizationFailed)
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var certificateInvalid x509.CertificateInvalidError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostnameError) || errors.As(err, &certificateInvalid) {
		return newError(CodeTLSVerificationFailed)
	}
	return newError(CodeConnectionFailed)
}
