package cluster

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
)

type OnboardingService struct {
	validator          *ValidationService
	connector          Connector
	cipher             CredentialCipher
	repository         ClusterRepository
	eventRepository    EventRepository
	capacityRepo       CapacityRepository
	classificationRepo CapacityClassificationRepository
	systemNamespaces   []string
	resourceCacheMu    sync.Mutex
	resourceCache      map[string]cachedResources
	storageStatsMu     sync.Mutex
	storageStatsAt     time.Time
	storageStats       EventStorageStats
	now                func() time.Time
}

type credentialEnvelope struct {
	CAData                []byte `json:"ca_data"`
	Token                 string `json:"token,omitempty"`
	ClientCertificateData []byte `json:"client_certificate_data,omitempty"`
	ClientKeyData         []byte `json:"client_key_data,omitempty"`
}

func NewOnboardingService(connector Connector, cipher CredentialCipher, repository ClusterRepository) *OnboardingService {
	service := &OnboardingService{
		validator:        NewValidationService(connector),
		connector:        connector,
		cipher:           cipher,
		repository:       repository,
		now:              time.Now,
		resourceCache:    make(map[string]cachedResources),
		systemNamespaces: []string{"kube-system", "cmb-monitoring"},
	}
	if capacityRepo, ok := repository.(CapacityRepository); ok {
		service.capacityRepo = capacityRepo
	}
	if classificationRepo, ok := repository.(CapacityClassificationRepository); ok {
		service.classificationRepo = classificationRepo
	}
	return service
}

func (service *OnboardingService) SetSystemNamespaces(namespaces []string) {
	values := make([]string, 0, len(namespaces))
	seen := make(map[string]struct{}, len(namespaces))
	for _, namespace := range namespaces {
		namespace = strings.TrimSpace(namespace)
		if namespace == "" {
			continue
		}
		if _, exists := seen[namespace]; exists {
			continue
		}
		seen[namespace] = struct{}{}
		values = append(values, namespace)
	}
	if len(values) > 0 {
		service.systemNamespaces = values
	}
}

func (service *OnboardingService) GetCapacity(ctx context.Context, id string) (ClusterCapacity, error) {
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return ClusterCapacity{}, err
	}
	return service.capacityForRecord(ctx, record)
}

func (service *OnboardingService) capacityForRecord(ctx context.Context, record ClusterRecord) (ClusterCapacity, error) {
	classification, err := service.activeCapacityClassification(ctx)
	if err != nil {
		return ClusterCapacity{}, err
	}
	return service.capacityForRecordWithClassification(ctx, record, classification)
}

func (service *OnboardingService) capacityForRecordWithClassification(ctx context.Context, record ClusterRecord, classification CapacityClassification) (ClusterCapacity, error) {
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return ClusterCapacity{}, err
	}
	connector, ok := service.connector.(CapacityConnector)
	if !ok {
		return ClusterCapacity{}, newError(CodeConnectionFailed)
	}
	rule := capacityRuleForStack(classification, record.TechnologyStack)
	snapshot, err := connector.Capacity(ctx, parsed, rule)
	if err != nil {
		return ClusterCapacity{}, err
	}
	if service.classificationRepo != nil {
		observedAt := service.now().UTC()
		observations := make([]NamespaceObservation, 0, len(snapshot.Namespaces))
		for _, namespace := range snapshot.Namespaces {
			observations = append(observations, NamespaceObservation{TechnologyStack: record.TechnologyStack, Namespace: namespace.Namespace, FirstSeenAt: observedAt, LastSeenAt: observedAt})
		}
		if err := service.classificationRepo.ObserveNamespaces(ctx, observations); err != nil {
			return ClusterCapacity{}, err
		}
	}
	if service.capacityRepo == nil {
		return snapshot, nil
	}
	baseline, err := service.capacityRepo.LatestCapacityBaseline(ctx, record.ID)
	if err != nil {
		return ClusterCapacity{}, err
	}
	snapshot.NewPlatformNamespaces = []string{}
	if baseline != nil {
		known := make(map[string]struct{}, len(baseline.SystemNamespaces))
		for _, name := range baseline.SystemNamespaces {
			known[name] = struct{}{}
		}
		// Legacy baselines may contain only explicitly classified platform namespaces.
		// Only creation after delivery proves a namespace is a post-delivery addition.
		for _, namespace := range snapshot.Namespaces {
			if _, exists := known[namespace.Namespace]; !exists && namespace.Class == "platform" && namespace.CreatedAt.After(baseline.CapturedAt) && !baseline.CapturedAt.IsZero() {
				snapshot.NewPlatformNamespaces = append(snapshot.NewPlatformNamespaces, namespace.Namespace)
			}
		}
		sort.Strings(snapshot.NewPlatformNamespaces)
	}
	snapshot.Baseline = baseline
	snapshot.Comparison = compareCapacity(snapshot, baseline)
	snapshot.BaselineReview = CapacityBaselineReview{Status: "not_required"}
	if baseline != nil && !sameStrings(baseline.SystemNamespaces, snapshot.SystemNamespaces) {
		snapshot.BaselineReview.Status = "pending"
		if service.classificationRepo != nil {
			decision, err := service.classificationRepo.LatestCapacityBaselineDecision(ctx, record.ID, classification.ID)
			if err != nil {
				return ClusterCapacity{}, err
			}
			if decision != nil && decision.Decision == "keep" {
				snapshot.BaselineReview = CapacityBaselineReview{Status: "kept", Reason: decision.Reason}
			}
		}
	}
	return snapshot, nil
}

func (service *OnboardingService) GetFleetCapacity(ctx context.Context) (FleetCapacity, error) {
	if repository, ok := service.repository.(FleetCapacityRepository); ok {
		return repository.ListFleetCapacitySnapshots(ctx)
	}
	return service.collectFleetCapacity(ctx)
}

func (service *OnboardingService) collectFleetCapacity(ctx context.Context) (FleetCapacity, error) {
	records, err := service.repository.ListRecords(ctx)
	if err != nil {
		return FleetCapacity{}, err
	}
	classification, err := service.activeCapacityClassification(ctx)
	if err != nil {
		return FleetCapacity{}, err
	}
	type result struct {
		capacity ClusterCapacity
		err      error
	}
	results := make([]result, len(records))
	semaphore := make(chan struct{}, 6)
	var waitGroup sync.WaitGroup
	for index := range records {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index].err = ctx.Err()
				return
			}
			results[index].capacity, results[index].err = service.capacityForRecordWithClassification(ctx, records[index], classification)
		}()
	}
	waitGroup.Wait()

	fleet := FleetCapacity{
		CollectedAt: service.now().UTC(),
		Items:       make([]FleetCapacityItem, 0, len(records)),
		Failures:    make([]FleetCapacityFailure, 0),
	}
	for index, record := range records {
		metadata := record.ManagedCluster
		if results[index].err != nil {
			fleet.Failures = append(fleet.Failures, FleetCapacityFailure{
				ClusterID: metadata.ID, ClusterName: metadata.Name, Environment: metadata.Environment,
				ClusterType: metadata.ClusterType, TechnologyStack: metadata.TechnologyStack, Code: ErrorCode(results[index].err),
			})
			continue
		}
		fleet.Items = append(fleet.Items, FleetCapacityItem{
			ClusterID: metadata.ID, ClusterName: metadata.Name, Environment: metadata.Environment,
			ClusterType: metadata.ClusterType, TechnologyStack: metadata.TechnologyStack, Capacity: results[index].capacity,
		})
	}
	return fleet, nil
}

func (service *OnboardingService) activeCapacityClassification(ctx context.Context) (CapacityClassification, error) {
	if service.classificationRepo != nil {
		value, err := service.classificationRepo.LatestCapacityClassification(ctx)
		if err != nil {
			return CapacityClassification{}, err
		}
		if value != nil {
			return *value, nil
		}
	}
	return CapacityClassification{BusinessNamespaces: []string{}, CommonPlatformNamespaces: []string{}, StackPlatformNamespaces: map[string][]string{}}, nil
}

func capacityRuleForStack(value CapacityClassification, _ string) CapacityRule {
	// Legacy platform lists remain in historical versions only. All non-business namespaces are platform.
	return CapacityRule{VersionID: value.ID, BusinessNamespaces: normalizedNamespaces(value.BusinessNamespaces)}
}

func normalizedNamespaces(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sameStrings(left, right []string) bool {
	left, right = normalizedNamespaces(left), normalizedNamespaces(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (service *OnboardingService) SaveCapacityClassification(ctx context.Context, value CapacityClassification) (CapacityClassification, error) {
	if service.classificationRepo == nil {
		return CapacityClassification{}, newError(CodePersistenceFailed)
	}
	value.BusinessNamespaces = normalizedNamespaces(value.BusinessNamespaces)
	for _, namespace := range value.BusinessNamespaces {
		if len(validation.IsDNS1123Label(namespace)) > 0 {
			return CapacityClassification{}, newError(CodeCapacityRulesInvalid)
		}
	}
	value.CommonPlatformNamespaces = []string{}
	value.StackPlatformNamespaces = map[string][]string{}
	value.ID = 0
	value.CreatedAt = service.now().UTC()
	return service.classificationRepo.CreateCapacityClassification(ctx, value)
}

func (service *OnboardingService) GetCapacityClassification(ctx context.Context) (CapacityClassificationOverview, error) {
	classification, err := service.activeCapacityClassification(ctx)
	if err != nil {
		return CapacityClassificationOverview{}, err
	}
	fleet, err := service.GetFleetCapacity(ctx)
	if err != nil {
		return CapacityClassificationOverview{}, err
	}
	type aggregate struct {
		item     DiscoveredNamespace
		clusters map[string]struct{}
	}
	values := make(map[string]*aggregate)
	for _, clusterItem := range fleet.Items {
		for _, namespace := range clusterItem.Capacity.Namespaces {
			key := namespace.Namespace
			current := values[key]
			if current == nil {
				current = &aggregate{item: DiscoveredNamespace{Namespace: namespace.Namespace, Class: namespace.Class}, clusters: map[string]struct{}{}}
				values[key] = current
			}
			current.clusters[clusterItem.ClusterID] = struct{}{}
			current.item.Pods += namespace.Pods
			current.item.CPURequestsMilli += namespace.CPURequestsMilli
			current.item.MemoryRequests += namespace.MemoryRequests
		}
	}
	firstSeen := make(map[string]time.Time)
	if service.classificationRepo != nil {
		observations, err := service.classificationRepo.ListNamespaceObservations(ctx)
		if err != nil {
			return CapacityClassificationOverview{}, err
		}
		for _, observation := range observations {
			key := observation.Namespace
			if previous, exists := firstSeen[key]; !exists || observation.FirstSeenAt.Before(previous) {
				firstSeen[key] = observation.FirstSeenAt
			}
		}
	}
	discovered := make([]DiscoveredNamespace, 0, len(values))
	for key, value := range values {
		value.item.ClusterCount = len(value.clusters)
		value.item.FirstSeenAt = firstSeen[key]
		discovered = append(discovered, value.item)
	}
	sort.Slice(discovered, func(i, j int) bool {
		if discovered[i].TechnologyStack != discovered[j].TechnologyStack {
			return discovered[i].TechnologyStack < discovered[j].TechnologyStack
		}
		return discovered[i].Namespace < discovered[j].Namespace
	})
	return CapacityClassificationOverview{Classification: classification, Discovered: discovered}, nil
}

func (service *OnboardingService) KeepCapacityBaseline(ctx context.Context, clusterID, reason string) (CapacityBaselineDecision, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return CapacityBaselineDecision{}, newError(CodeBaselineReasonRequired)
	}
	if service.classificationRepo == nil {
		return CapacityBaselineDecision{}, newError(CodePersistenceFailed)
	}
	if _, err := service.repository.GetRecord(ctx, clusterID); err != nil {
		return CapacityBaselineDecision{}, err
	}
	classification, err := service.activeCapacityClassification(ctx)
	if err != nil {
		return CapacityBaselineDecision{}, err
	}
	return service.classificationRepo.CreateCapacityBaselineDecision(ctx, CapacityBaselineDecision{ClusterID: clusterID, ClassificationVersionID: classification.ID, Decision: "keep", Reason: reason, CreatedAt: service.now().UTC()})
}

func (service *OnboardingService) LockCapacityBaseline(ctx context.Context, id string) (CapacityBaseline, error) {
	if service.capacityRepo == nil {
		return CapacityBaseline{}, newError(CodePersistenceFailed)
	}
	snapshot, err := service.GetCapacity(ctx, id)
	if err != nil {
		return CapacityBaseline{}, err
	}
	baseline := CapacityBaseline{
		ClusterID: id, CapturedAt: service.now().UTC(), SystemNamespaces: snapshot.SystemNamespaces,
		WorkerNodes: snapshot.WorkerNodes, CPUAllocatableMilli: snapshot.CPUAllocatableMilli,
		MemoryAllocatable:   snapshot.MemoryAllocatable,
		PlatformCPURequests: snapshot.Platform.CPURequestsMilli, PlatformCPULimits: snapshot.Platform.CPULimitsMilli,
		PlatformMemoryRequest: snapshot.Platform.MemoryRequests, PlatformMemoryLimit: snapshot.Platform.MemoryLimits,
	}
	return service.capacityRepo.CreateCapacityBaseline(ctx, baseline)
}

func compareCapacity(current ClusterCapacity, baseline *CapacityBaseline) CapacityComparison {
	if baseline == nil {
		return CapacityComparison{Status: "not_set"}
	}
	comparison := CapacityComparison{
		Status:                   "stable",
		CPUAllocatableChange:     percentChange(current.CPUAllocatableMilli, baseline.CPUAllocatableMilli),
		MemoryAllocatableChange:  percentChange(current.MemoryAllocatable, baseline.MemoryAllocatable),
		PlatformCPURequestChange: percentChange(current.Platform.CPURequestsMilli, baseline.PlatformCPURequests),
		PlatformMemoryChange:     percentChange(current.Platform.MemoryRequests, baseline.PlatformMemoryRequest),
	}
	if math.Abs(comparison.CPUAllocatableChange) > 5 || math.Abs(comparison.MemoryAllocatableChange) > 5 {
		comparison.Status = "rebaseline"
	} else if math.Abs(comparison.PlatformCPURequestChange) > 5 || math.Abs(comparison.PlatformMemoryChange) > 5 {
		comparison.Status = "drift"
	}
	return comparison
}

func percentChange(current, baseline int64) float64 {
	if baseline == 0 {
		if current == 0 {
			return 0
		}
		return 100
	}
	return (float64(current-baseline) / float64(baseline)) * 100
}

func (service *OnboardingService) Validate(ctx context.Context, name string, data []byte) (ValidationPreview, error) {
	return service.validator.Validate(ctx, name, data)
}

func (service *OnboardingService) Onboard(ctx context.Context, name string, environment string, clusterType string, technologyStack string, data []byte) (ManagedCluster, error) {
	environment = strings.TrimSpace(environment)
	if environment == "" {
		return ManagedCluster{}, newError(CodeEnvironmentRequired)
	}
	clusterType = strings.ToUpper(strings.TrimSpace(clusterType))
	if clusterType == "" {
		return ManagedCluster{}, newError(CodeClusterTypeRequired)
	}
	if clusterType != "G01" && clusterType != "OCP" {
		return ManagedCluster{}, newError(CodeClusterTypeInvalid)
	}
	technologyStack = strings.ToUpper(strings.TrimSpace(technologyStack))
	if technologyStack == "" {
		return ManagedCluster{}, newError(CodeTechnologyStackRequired)
	}
	if technologyStack != "TCS" && technologyStack != "CMBK8S" && technologyStack != "OCP" {
		return ManagedCluster{}, newError(CodeTechnologyStackInvalid)
	}
	if clusterType == "G01" && technologyStack != "TCS" && technologyStack != "CMBK8S" || clusterType == "OCP" && technologyStack != "OCP" {
		return ManagedCluster{}, newError(CodeClusterTypeMismatch)
	}
	parsed, err := ParseKubeconfig(data)
	if err != nil {
		return ManagedCluster{}, err
	}
	preview, err := service.validator.Validate(ctx, name, data)
	if err != nil {
		return ManagedCluster{}, err
	}

	credentialJSON, err := json.Marshal(credentialEnvelope{
		CAData:                parsed.CAData,
		Token:                 parsed.Token,
		ClientCertificateData: parsed.ClientCertificateData,
		ClientKeyData:         parsed.ClientKeyData,
	})
	if err != nil {
		return ManagedCluster{}, newError(CodeEncryptionFailed)
	}
	encryptedCredentials, err := service.cipher.Encrypt(credentialJSON)
	if err != nil {
		return ManagedCluster{}, newError(CodeEncryptionFailed)
	}

	id, err := randomID()
	if err != nil {
		return ManagedCluster{}, newError(CodePersistenceFailed)
	}
	now := service.now().UTC()
	managedCluster := ManagedCluster{
		ID:                 id,
		Name:               preview.Name,
		Environment:        environment,
		ClusterType:        clusterType,
		TechnologyStack:    technologyStack,
		APIServer:          preview.APIServer,
		ContextName:        preview.ContextName,
		KubernetesVersion:  preview.KubernetesVersion,
		AuthenticationType: preview.AuthenticationType,
		ConnectionStatus:   "connected",
		CreatedAt:          now,
		LastCheckedAt:      now,
	}
	return service.repository.Create(ctx, ClusterRecord{
		ManagedCluster:       managedCluster,
		EncryptedCredentials: encryptedCredentials,
		Fingerprint:          clusterFingerprint(parsed),
	})
}

func (service *OnboardingService) List(ctx context.Context) ([]ManagedCluster, error) {
	records, err := service.repository.ListRecords(ctx)
	if err != nil {
		return nil, err
	}
	managedClusters := make([]ManagedCluster, len(records))
	for index := range records {
		managedClusters[index] = records[index].ManagedCluster
	}
	return managedClusters, nil
}

func (service *OnboardingService) Delete(ctx context.Context, id string) error {
	if service.eventRepository != nil {
		if err := service.eventRepository.DeleteClusterEvents(ctx, id); err != nil {
			return err
		}
		service.storageStatsMu.Lock()
		service.storageStatsAt = time.Time{}
		service.storageStatsMu.Unlock()
	}
	return service.repository.Delete(ctx, id)
}

func (service *OnboardingService) UpdateClassification(ctx context.Context, id string, clusterType string, technologyStack string) (ManagedCluster, error) {
	clusterType = strings.ToUpper(strings.TrimSpace(clusterType))
	technologyStack = strings.ToUpper(strings.TrimSpace(technologyStack))
	if clusterType == "" {
		return ManagedCluster{}, newError(CodeClusterTypeRequired)
	}
	if clusterType != "G01" && clusterType != "OCP" {
		return ManagedCluster{}, newError(CodeClusterTypeInvalid)
	}
	if technologyStack == "" {
		return ManagedCluster{}, newError(CodeTechnologyStackRequired)
	}
	if technologyStack != "TCS" && technologyStack != "CMBK8S" && technologyStack != "OCP" {
		return ManagedCluster{}, newError(CodeTechnologyStackInvalid)
	}
	if clusterType == "G01" && technologyStack != "TCS" && technologyStack != "CMBK8S" || clusterType == "OCP" && technologyStack != "OCP" {
		return ManagedCluster{}, newError(CodeClusterTypeMismatch)
	}
	repository, ok := service.repository.(ClusterClassificationRepository)
	if !ok {
		return ManagedCluster{}, newError(CodePersistenceFailed)
	}
	if err := repository.UpdateClusterClassification(ctx, id, clusterType, technologyStack); err != nil {
		return ManagedCluster{}, err
	}
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return ManagedCluster{}, err
	}
	return record.ManagedCluster, nil
}

func (service *OnboardingService) Get(ctx context.Context, id string) (ManagedCluster, error) {
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return ManagedCluster{}, err
	}
	return service.inspectRecord(ctx, record), nil
}

func (service *OnboardingService) GetNodes(ctx context.Context, id string) ([]ClusterNode, error) {
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return nil, err
	}
	return service.connector.Nodes(ctx, parsed)
}

func (service *OnboardingService) GetWorkloads(ctx context.Context, id string) ([]ClusterWorkload, error) {
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return nil, err
	}
	return service.connector.Workloads(ctx, parsed)
}

func (service *OnboardingService) GetPods(ctx context.Context, id string) ([]ClusterPod, error) {
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return nil, err
	}
	return service.connector.Pods(ctx, parsed)
}

func (service *OnboardingService) GetPod(ctx context.Context, id string, namespace string, name string) (ClusterPodDetail, error) {
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return ClusterPodDetail{}, err
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return ClusterPodDetail{}, err
	}
	return service.connector.Pod(ctx, parsed, namespace, name)
}

func (service *OnboardingService) GetPodLogs(ctx context.Context, id string, namespace string, name string, options PodLogOptions) (PodLogSnapshot, error) {
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return PodLogSnapshot{}, err
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return PodLogSnapshot{}, err
	}
	return service.connector.PodLogs(ctx, parsed, namespace, name, options)
}

func (service *OnboardingService) ExecPod(ctx context.Context, id, namespace, name string, options PodExecOptions) error {
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return err
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return err
	}
	executor, ok := service.connector.(PodExecConnector)
	if !ok {
		return newError(CodePodExecUnavailable)
	}
	return executor.ExecPod(ctx, parsed, namespace, name, options)
}

func (service *OnboardingService) GetWorkload(ctx context.Context, id string, kind string, namespace string, name string) (ClusterWorkloadDetail, error) {
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return ClusterWorkloadDetail{}, err
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return ClusterWorkloadDetail{}, err
	}
	return service.connector.Workload(ctx, parsed, kind, namespace, name)
}

func (service *OnboardingService) GetWorkloadYAML(ctx context.Context, id, kind, namespace, name string) (WorkloadYAML, error) {
	record, err := service.repository.GetRecord(ctx, id)
	if err != nil {
		return WorkloadYAML{}, err
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return WorkloadYAML{}, err
	}
	reader, ok := service.connector.(WorkloadYAMLConnector)
	if !ok {
		return WorkloadYAML{}, newError(CodeConnectionFailed)
	}
	return reader.WorkloadYAML(ctx, parsed, kind, namespace, name)
}

func (service *OnboardingService) parsedCredentials(record ClusterRecord) (ParsedKubeconfig, error) {
	credentialJSON, err := service.cipher.Decrypt(record.EncryptedCredentials)
	if err != nil {
		return ParsedKubeconfig{}, newError(CodeEncryptionFailed)
	}
	var credentials credentialEnvelope
	if err := json.Unmarshal(credentialJSON, &credentials); err != nil {
		return ParsedKubeconfig{}, newError(CodeEncryptionFailed)
	}
	return ParsedKubeconfig{
		APIServer:             record.APIServer,
		CAData:                credentials.CAData,
		Token:                 credentials.Token,
		ClientCertificateData: credentials.ClientCertificateData,
		ClientKeyData:         credentials.ClientKeyData,
	}, nil
}

func (service *OnboardingService) inspectRecord(ctx context.Context, record ClusterRecord) ManagedCluster {
	managed := record.ManagedCluster
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		managed.ConnectionStatus = "unreachable"
		return managed
	}
	snapshot, err := service.connector.Health(ctx, parsed)
	if err != nil {
		managed.ConnectionStatus = "unreachable"
		return managed
	}
	managed.NodesReady = intPointer(snapshot.NodesReady)
	managed.NodesTotal = intPointer(snapshot.NodesTotal)
	managed.InstancesReady = intPointer(snapshot.InstancesReady)
	managed.InstancesTotal = intPointer(snapshot.InstancesTotal)
	managed.CoreComponentsReady = intPointer(snapshot.CoreComponentsReady)
	managed.CoreComponentsTotal = intPointer(snapshot.CoreComponentsTotal)
	managed.WorkerNodes = intPointer(snapshot.WorkerNodes)
	managed.WorkerCores = int64Pointer(snapshot.WorkerCores)
	managed.APILatencyMS = int64Pointer(snapshot.APILatencyMS)
	managed.CPUUtilization = snapshot.CPUUtilization
	managed.MemoryUtilization = snapshot.MemoryUtilization
	managed.MetricsAvailable = snapshot.MetricsAvailable
	managed.ConnectionStatus = "connected"
	managed.LastCheckedAt = service.now().UTC()
	return managed
}

func (service *OnboardingService) refreshHealth(ctx context.Context, record ClusterRecord) error {
	repository, ok := service.repository.(ClusterHealthRepository)
	if !ok {
		return nil
	}
	return repository.UpdateClusterHealth(ctx, service.inspectRecord(ctx, record))
}

func intPointer(value int) *int { return &value }

func int64Pointer(value int64) *int64 { return &value }

func clusterFingerprint(parsed ParsedKubeconfig) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(parsed.APIServer))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(parsed.CAData)
	return hex.EncodeToString(hash.Sum(nil))
}

func randomID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
