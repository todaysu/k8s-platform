package store

import (
	"example.com/k8s-platform/internal/cluster"
	"strings"
)

func extraEventConditions(q cluster.EventQuery) ([]string, []any) {
	conditions := []string{}
	args := []any{}
	if q.ObjectUID != "" {
		conditions = append(conditions, "regarding_uid = ?")
		args = append(args, q.ObjectUID)
	}
	if q.ObjectName != "" {
		conditions = append(conditions, "regarding_name = ?")
		args = append(args, q.ObjectName)
	}
	if q.AttentionOnly {
		conditions = append(conditions, `(LOWER(type)='warning' OR LOWER(reason) LIKE '%failed%' OR LOWER(reason) IN ('backoff','unhealthy','evicted','nodeunavailable','nodenotready','imagepullbackoff','errimagepull'))`)
	}
	if q.Scope != "" {
		if len(q.BusinessNamespaces) == 0 {
			if q.Scope == "business" {
				conditions = append(conditions, "1=0")
			}
		} else {
			marks := strings.TrimSuffix(strings.Repeat("?,", len(q.BusinessNamespaces)), ",")
			if q.Scope == "business" {
				conditions = append(conditions, "(regarding_kind <> 'Node' AND regarding_namespace IN ("+marks+"))")
			} else {
				conditions = append(conditions, "(regarding_kind = 'Node' OR regarding_namespace NOT IN ("+marks+"))")
			}
			for _, name := range q.BusinessNamespaces {
				args = append(args, name)
			}
		}
	}
	return conditions, args
}
