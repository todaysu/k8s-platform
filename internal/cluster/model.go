package cluster

import (
	"context"
	"io"
	"time"
)

type TerminalSize struct {
	Width  uint16
	Height uint16
}
type PodExecOptions struct {
	Container string
	Shell     string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	Sizes     <-chan TerminalSize
}

type ManagedCluster struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Environment         string    `json:"environment"`
	ClusterType         string    `json:"cluster_type"`
	TechnologyStack     string    `json:"technology_stack"`
	APIServer           string    `json:"api_server"`
	ContextName         string    `json:"context_name"`
	KubernetesVersion   string    `json:"kubernetes_version"`
	AuthenticationType  string    `json:"authentication_type"`
	ConnectionStatus    string    `json:"connection_status"`
	CreatedAt           time.Time `json:"created_at"`
	LastCheckedAt       time.Time `json:"last_checked_at"`
	NodesReady          *int      `json:"nodes_ready"`
	NodesTotal          *int      `json:"nodes_total"`
	InstancesReady      *int      `json:"instances_ready"`
	InstancesTotal      *int      `json:"instances_total"`
	CoreComponentsReady *int      `json:"core_components_ready"`
	CoreComponentsTotal *int      `json:"core_components_total"`
	WorkerNodes         *int      `json:"worker_nodes"`
	WorkerCores         *int64    `json:"worker_cores"`
	APILatencyMS        *int64    `json:"api_latency_ms"`
	CPUUtilization      *float64  `json:"cpu_utilization"`
	MemoryUtilization   *float64  `json:"memory_utilization"`
	MetricsAvailable    bool      `json:"metrics_available"`
}

type ClusterNode struct {
	Name              string    `json:"name"`
	Status            string    `json:"status"`
	Roles             []string  `json:"roles"`
	Region            string    `json:"region"`
	Zone              string    `json:"zone"`
	InstanceType      string    `json:"instance_type"`
	CPUCores          float64   `json:"cpu_cores"`
	MemoryBytes       int64     `json:"memory_bytes"`
	KubernetesVersion string    `json:"kubernetes_version"`
	Schedulable       bool      `json:"schedulable"`
	CreatedAt         time.Time `json:"created_at"`
}

type CapacityValues struct {
	CPURequestsMilli          int64  `json:"cpu_requests_milli"`
	CPULimitsMilli            int64  `json:"cpu_limits_milli"`
	CPUUsageMilli             *int64 `json:"cpu_usage_milli"`
	MemoryRequests            int64  `json:"memory_requests_bytes"`
	MemoryLimits              int64  `json:"memory_limits_bytes"`
	MemoryUsage               *int64 `json:"memory_usage_bytes"`
	PodsWithoutCPURequests    int    `json:"pods_without_cpu_requests"`
	PodsWithoutMemoryRequests int    `json:"pods_without_memory_requests"`
}

type NamespaceCapacity struct {
	CreatedAt time.Time `json:"created_at"`
	Namespace string    `json:"namespace"`
	Class     string    `json:"class"`
	Pods      int       `json:"pods"`
	CapacityValues
}

type CapacityRule struct {
	VersionID          int64    `json:"version_id"`
	BusinessNamespaces []string `json:"business_namespaces"`
	PlatformNamespaces []string `json:"platform_namespaces"` // Deprecated: ignored; platform is the complement of business.
}

type CapacityClassification struct {
	ID                       int64               `json:"id"`
	CreatedAt                time.Time           `json:"created_at"`
	BusinessNamespaces       []string            `json:"business_namespaces"`
	CommonPlatformNamespaces []string            `json:"common_platform_namespaces"` // Historical storage compatibility only.
	StackPlatformNamespaces  map[string][]string `json:"stack_platform_namespaces"`
	Note                     string              `json:"note"`
}

type NamespaceObservation struct {
	TechnologyStack string    `json:"technology_stack"`
	Namespace       string    `json:"namespace"`
	FirstSeenAt     time.Time `json:"first_seen_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
}

type DiscoveredNamespace struct {
	TechnologyStack  string    `json:"technology_stack"`
	Namespace        string    `json:"namespace"`
	Class            string    `json:"class"`
	ClusterCount     int       `json:"cluster_count"`
	Pods             int       `json:"pods"`
	CPURequestsMilli int64     `json:"cpu_requests_milli"`
	MemoryRequests   int64     `json:"memory_requests_bytes"`
	FirstSeenAt      time.Time `json:"first_seen_at"`
}

type CapacityClassificationOverview struct {
	Classification CapacityClassification `json:"classification"`
	Discovered     []DiscoveredNamespace  `json:"discovered"`
}

type CapacityBaselineDecision struct {
	ID                      int64     `json:"id"`
	ClusterID               string    `json:"cluster_id"`
	ClassificationVersionID int64     `json:"classification_version_id"`
	Decision                string    `json:"decision"`
	Reason                  string    `json:"reason"`
	CreatedAt               time.Time `json:"created_at"`
}

type CapacityBaselineReview struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type CapacityBaseline struct {
	ID                    int64     `json:"id"`
	ClusterID             string    `json:"cluster_id"`
	CapturedAt            time.Time `json:"captured_at"`
	SystemNamespaces      []string  `json:"system_namespaces"`
	WorkerNodes           int       `json:"worker_nodes"`
	CPUAllocatableMilli   int64     `json:"cpu_allocatable_milli"`
	MemoryAllocatable     int64     `json:"memory_allocatable_bytes"`
	PlatformCPURequests   int64     `json:"platform_cpu_requests_milli"`
	PlatformCPULimits     int64     `json:"platform_cpu_limits_milli"`
	PlatformMemoryRequest int64     `json:"platform_memory_requests_bytes"`
	PlatformMemoryLimit   int64     `json:"platform_memory_limits_bytes"`
}

type CapacityComparison struct {
	Status                   string  `json:"status"`
	CPUAllocatableChange     float64 `json:"cpu_allocatable_change_percent"`
	MemoryAllocatableChange  float64 `json:"memory_allocatable_change_percent"`
	PlatformCPURequestChange float64 `json:"platform_cpu_request_change_percent"`
	PlatformMemoryChange     float64 `json:"platform_memory_request_change_percent"`
}

type ClusterCapacity struct {
	NewPlatformNamespaces   []string               `json:"new_platform_namespaces"`
	CollectedAt             time.Time              `json:"collected_at"`
	SystemNamespaces        []string               `json:"system_namespaces"`
	WorkerNodes             int                    `json:"worker_nodes"`
	CPUAllocatableMilli     int64                  `json:"cpu_allocatable_milli"`
	MemoryAllocatable       int64                  `json:"memory_allocatable_bytes"`
	SafetyReservePercent    float64                `json:"safety_reserve_percent"`
	MetricsAvailable        bool                   `json:"metrics_available"`
	Platform                CapacityValues         `json:"platform"`
	Business                CapacityValues         `json:"business"`
	Unclassified            CapacityValues         `json:"unclassified"`
	Pending                 CapacityValues         `json:"pending"`
	Namespaces              []NamespaceCapacity    `json:"namespaces"`
	Baseline                *CapacityBaseline      `json:"baseline"`
	Comparison              CapacityComparison     `json:"comparison"`
	ClassificationVersionID int64                  `json:"classification_version_id"`
	BaselineReview          CapacityBaselineReview `json:"baseline_review"`
}

// FleetCapacity keeps per-cluster snapshots intact so the UI can aggregate
// physical quantities first and only then calculate percentages.
type FleetCapacity struct {
	CollectedAt time.Time              `json:"collected_at"`
	Items       []FleetCapacityItem    `json:"items"`
	Failures    []FleetCapacityFailure `json:"failures"`
}

type FleetCapacityItem struct {
	ClusterID       string          `json:"cluster_id"`
	ClusterName     string          `json:"cluster_name"`
	Environment     string          `json:"environment"`
	ClusterType     string          `json:"cluster_type"`
	TechnologyStack string          `json:"technology_stack"`
	Capacity        ClusterCapacity `json:"capacity"`
}

type FleetCapacityFailure struct {
	ClusterID       string `json:"cluster_id"`
	ClusterName     string `json:"cluster_name"`
	Environment     string `json:"environment"`
	ClusterType     string `json:"cluster_type"`
	TechnologyStack string `json:"technology_stack"`
	Code            string `json:"code"`
}

type ClusterWorkload struct {
	Kind          string    `json:"kind"`
	Namespace     string    `json:"namespace"`
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	StatusReason  string    `json:"status_reason"`
	Desired       int32     `json:"desired"`
	Ready         int32     `json:"ready"`
	Available     int32     `json:"available"`
	Updated       int32     `json:"updated"`
	PodsReady     int       `json:"pods_ready"`
	PodsTotal     int       `json:"pods_total"`
	RestartCount  int32     `json:"restart_count"`
	Images        []string  `json:"images"`
	CreatedAt     time.Time `json:"created_at"`
	LastUpdatedAt time.Time `json:"last_updated_at"`
}

type WorkloadPod struct {
	Name            string     `json:"name"`
	Phase           string     `json:"phase"`
	StatusReason    string     `json:"status_reason"`
	ReadyContainers int        `json:"ready_containers"`
	TotalContainers int        `json:"total_containers"`
	RestartCount    int32      `json:"restart_count"`
	NodeName        string     `json:"node_name"`
	PodIP           string     `json:"pod_ip"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at"`
}

type ClusterPod struct {
	Name            string     `json:"name"`
	Namespace       string     `json:"namespace"`
	Phase           string     `json:"phase"`
	StatusReason    string     `json:"status_reason"`
	ReadyContainers int        `json:"ready_containers"`
	TotalContainers int        `json:"total_containers"`
	RestartCount    int32      `json:"restart_count"`
	OwnerKind       string     `json:"owner_kind"`
	OwnerName       string     `json:"owner_name"`
	NodeName        string     `json:"node_name"`
	PodIP           string     `json:"pod_ip"`
	Images          []string   `json:"images"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at"`
}

type PodContainer struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restart_count"`
	State        string `json:"state"`
	StatusReason string `json:"status_reason"`
}

type PodCondition struct {
	Type               string     `json:"type"`
	Status             string     `json:"status"`
	Reason             string     `json:"reason"`
	Message            string     `json:"message"`
	LastTransitionTime *time.Time `json:"last_transition_time"`
}

type ClusterPodDetail struct {
	ClusterPod
	Labels         map[string]string `json:"labels"`
	ServiceAccount string            `json:"service_account"`
	HostIP         string            `json:"host_ip"`
	QoSClass       string            `json:"qos_class"`
	Containers     []PodContainer    `json:"containers"`
	Conditions     []PodCondition    `json:"conditions"`
}

type PodLogSnapshot struct {
	Container string     `json:"container"`
	Text      string     `json:"text"`
	TailLines int64      `json:"tail_lines"`
	Previous  bool       `json:"previous"`
	Truncated bool       `json:"truncated"`
	SinceTime *time.Time `json:"since_time,omitempty"`
	UntilTime *time.Time `json:"until_time,omitempty"`
}

type PodLogOptions struct {
	Container string
	TailLines int64
	Previous  bool
	SinceTime *time.Time
	UntilTime *time.Time
}

type ClusterEvent struct {
	ClusterID           string    `json:"cluster_id"`
	EventUID            string    `json:"event_uid"`
	Namespace           string    `json:"namespace"`
	Type                string    `json:"type"`
	Reason              string    `json:"reason"`
	Action              string    `json:"action"`
	Message             string    `json:"message"`
	RegardingAPIVersion string    `json:"regarding_api_version"`
	RegardingKind       string    `json:"regarding_kind"`
	RegardingNamespace  string    `json:"regarding_namespace"`
	RegardingName       string    `json:"regarding_name"`
	RegardingUID        string    `json:"regarding_uid"`
	ReportingController string    `json:"reporting_controller"`
	ReportingInstance   string    `json:"reporting_instance"`
	OccurrenceCount     int64     `json:"occurrence_count"`
	FirstObservedAt     time.Time `json:"first_observed_at"`
	LastObservedAt      time.Time `json:"last_observed_at"`
	ResourceVersion     string    `json:"resource_version"`
}

type EventBatch struct {
	APIVersion      string
	ResourceVersion string
	Items           []ClusterEvent
}

type EventQuery struct {
	Scope              string
	BusinessNamespaces []string
	AttentionOnly      bool
	ObjectUID          string
	ObjectName         string
	Since              time.Time
	Until              time.Time
	Type               string
	Namespace          string
	Kind               string
	Search             string
	ComponentOnly      bool
	Limit              int
	CursorTime         *time.Time
	CursorUID          string
}

type EventPage struct {
	Items      []ClusterEvent `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
	Total      int64          `json:"total"`
}

type EventSummary struct {
	Total             int64      `json:"total"`
	Warning           int64      `json:"warning"`
	Attention         int64      `json:"attention"`
	AffectedObjects   int64      `json:"affected_objects"`
	ComponentWarnings int64      `json:"component_warnings"`
	CollectorState    string     `json:"collector_state"`
	LastSuccessAt     *time.Time `json:"last_success_at,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
}

type EventFacets struct {
	Types      []string `json:"types"`
	Namespaces []string `json:"namespaces"`
	Kinds      []string `json:"kinds"`
}

type EventCollectorStatus struct {
	ClusterID       string `json:"cluster_id"`
	ClusterName     string `json:"cluster_name,omitempty"`
	State           string `json:"state"`
	ResourceVersion string
	LastSuccessAt   *time.Time `json:"last_success_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at,omitempty"`
}

type FleetEventQuery struct {
	View          string
	Issue         string
	Page          int
	IncludeNormal bool
	EventQuery
	Environment     string
	TechnologyStack string
	ClusterID       string
}

type FleetEvent struct {
	ClusterEvent
	ClusterName     string `json:"cluster_name"`
	Environment     string `json:"environment"`
	TechnologyStack string `json:"technology_stack"`
	Scope           string `json:"scope"`
}

type FleetEventIssue struct {
	Title            string    `json:"title"`
	Fingerprint      string    `json:"fingerprint"`
	Severity         string    `json:"severity"`
	Type             string    `json:"type"`
	Reason           string    `json:"reason"`
	RegardingKind    string    `json:"regarding_kind"`
	Message          string    `json:"message"`
	OccurrenceCount  int64     `json:"occurrence_count"`
	EventCount       int       `json:"event_count"`
	AffectedClusters int       `json:"affected_clusters"`
	AffectedObjects  int       `json:"affected_objects"`
	ClusterIDs       []string  `json:"cluster_ids"`
	FirstObservedAt  time.Time `json:"first_observed_at"`
	LastObservedAt   time.Time `json:"last_observed_at"`
}

type FleetEventSummary struct {
	EventRecords      int64 `json:"event_records"`
	ProblemGroups     int   `json:"problem_groups"`
	WarningGroups     int   `json:"warning_groups"`
	AffectedClusters  int   `json:"affected_clusters"`
	AffectedObjects   int   `json:"affected_objects"`
	CollectorsHealthy int   `json:"collectors_healthy"`
	CollectorsFailed  int   `json:"collectors_failed"`
	TotalClusters     int   `json:"total_clusters"`
}

type FleetEventOverview struct {
	Objects            []FleetEventObject     `json:"objects"`
	Pagination         EventPagination        `json:"pagination"`
	Failures           []FleetEventFailure    `json:"failures"`
	Since              time.Time              `json:"since"`
	Until              time.Time              `json:"until"`
	BusinessConfigured bool                   `json:"business_configured"`
	Items              []FleetEvent           `json:"items"`
	Issues             []FleetEventIssue      `json:"issues"`
	Summary            FleetEventSummary      `json:"summary"`
	Facets             EventFacets            `json:"facets"`
	Collectors         []EventCollectorStatus `json:"collectors"`
	Total              int64                  `json:"total"`
	Truncated          bool                   `json:"truncated"`
	Storage            EventStorageStats      `json:"storage"`
}

type EventStorageStats struct {
	RawRows              int64      `json:"raw_rows"`
	HourlyRows           int64      `json:"hourly_rows"`
	NormalRetentionDays  int        `json:"normal_retention_days"`
	WarningRetentionDays int        `json:"warning_retention_days"`
	RollupRetentionDays  int        `json:"rollup_retention_days"`
	LatestRollupAt       *time.Time `json:"latest_rollup_at,omitempty"`
}

type ClusterWorkloadDetail struct {
	ClusterWorkload
	Labels map[string]string `json:"labels"`
	Pods   []WorkloadPod     `json:"pods"`
}

type WorkloadYAML struct {
	YAML string `json:"yaml"`
}

type GlobalResourceResult struct {
	ClusterID       string   `json:"cluster_id"`
	ClusterName     string   `json:"cluster_name"`
	Environment     string   `json:"environment"`
	TechnologyStack string   `json:"technology_stack"`
	Kind            string   `json:"kind"`
	Namespace       string   `json:"namespace"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	OwnerKind       string   `json:"owner_kind,omitempty"`
	OwnerName       string   `json:"owner_name,omitempty"`
	Images          []string `json:"images,omitempty"`
}
type GlobalResourceSearch struct {
	Items          []GlobalResourceResult `json:"items"`
	FailedClusters []string               `json:"failed_clusters"`
	Truncated      bool                   `json:"truncated"`
}

type ClusterRecord struct {
	ManagedCluster
	EncryptedCredentials string
	Fingerprint          string
}

type ClusterRepository interface {
	Create(context.Context, ClusterRecord) (ManagedCluster, error)
	Delete(context.Context, string) error
	GetRecord(context.Context, string) (ClusterRecord, error)
	ListRecords(context.Context) ([]ClusterRecord, error)
}

// ClusterHealthRepository stores the latest fleet health snapshot. It is kept
// separate from ClusterRepository so lightweight repositories used by tests or
// integrations do not have to implement background collection.
type ClusterHealthRepository interface {
	UpdateClusterHealth(context.Context, ManagedCluster) error
}

type ClusterClassificationRepository interface {
	UpdateClusterClassification(context.Context, string, string, string) error
}

type EventRepository interface {
	UpsertEvents(context.Context, []ClusterEvent) error
	QueryEvents(context.Context, string, EventQuery) (EventPage, error)
	EventSummary(context.Context, string, time.Time, time.Time) (EventSummary, error)
	EventFacets(context.Context, string, time.Time, time.Time) (EventFacets, error)
	SetEventCollectorStatus(context.Context, EventCollectorStatus) error
	DeleteEventsByRetention(context.Context, time.Time, time.Time, int) (int64, error)
	DeleteEventRollupsBefore(context.Context, time.Time, int) (int64, error)
	EnsureEventRollups(context.Context) error
	EventStorageStats(context.Context) (EventStorageStats, error)
	DeleteClusterEvents(context.Context, string) error
}

type CapacityRepository interface {
	CreateCapacityBaseline(context.Context, CapacityBaseline) (CapacityBaseline, error)
	LatestCapacityBaseline(context.Context, string) (*CapacityBaseline, error)
}

type FleetCapacityRepository interface {
	SaveFleetCapacitySuccess(context.Context, FleetCapacityItem, time.Time) error
	SaveFleetCapacityFailure(context.Context, FleetCapacityFailure, time.Time) error
	ListFleetCapacitySnapshots(context.Context) (FleetCapacity, error)
}

type CapacityClassificationRepository interface {
	CreateCapacityClassification(context.Context, CapacityClassification) (CapacityClassification, error)
	LatestCapacityClassification(context.Context) (*CapacityClassification, error)
	ObserveNamespaces(context.Context, []NamespaceObservation) error
	ListNamespaceObservations(context.Context) ([]NamespaceObservation, error)
	CreateCapacityBaselineDecision(context.Context, CapacityBaselineDecision) (CapacityBaselineDecision, error)
	LatestCapacityBaselineDecision(context.Context, string, int64) (*CapacityBaselineDecision, error)
}

// EventScanner streams matching records without materializing all raw events.
type EventScanner interface {
	ScanEvents(context.Context, string, EventQuery, func(ClusterEvent) error) error
}
type EventPagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalItems int `json:"total_items"`
	TotalPages int `json:"total_pages"`
}
type FleetEventFailure struct {
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
	Code        string `json:"code"`
}
type FleetEventObject struct {
	GroupingNote    string             `json:"grouping_note,omitempty"`
	Members         []FleetEventObject `json:"members,omitempty"`
	ClusterID       string             `json:"cluster_id"`
	ClusterName     string             `json:"cluster_name"`
	Namespace       string             `json:"namespace"`
	Kind            string             `json:"kind"`
	Name            string             `json:"name"`
	UID             string             `json:"uid"`
	Scope           string             `json:"scope"`
	EventCount      int                `json:"event_count"`
	OccurrenceCount int64              `json:"occurrence_count"`
	Message         string             `json:"message"`
	LastObservedAt  time.Time          `json:"last_observed_at"`
}
type EventResourceReference struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	UID  string `json:"uid"`
}
type EventResourceQuery struct{ ClusterID, Namespace, Kind, Name, UID string }
type EventContainerEvidence struct {
	PreviousExitCode *int32 `json:"previous_exit_code,omitempty"`
	PreviousReason   string `json:"previous_reason,omitempty"`
	Name             string `json:"name"`
	State            string `json:"state"`
	Reason           string `json:"reason"`
	ExitCode         *int32 `json:"exit_code,omitempty"`
	Restarts         int32  `json:"restarts"`
	HasLogs          bool   `json:"has_logs"`
	HasPreviousLogs  bool   `json:"has_previous_logs"`
}
type EventResourceSnapshot struct {
	Status     string                   `json:"status"`
	Detail     string                   `json:"detail"`
	CheckedAt  time.Time                `json:"checked_at"`
	UID        string                   `json:"uid"`
	NodeName   string                   `json:"node_name"`
	Owners     []EventResourceReference `json:"owners"`
	Containers []EventContainerEvidence `json:"containers"`
}
type EventResourceConnector interface {
	EventResource(context.Context, ParsedKubeconfig, EventResourceQuery) (EventResourceSnapshot, error)
}
