package cluster

import (
	"context"
	"errors"
	"io"
	"net/http"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
)

const listPageSize = 500
const listMaxObjects = 25000
const listPageBytes = 8 << 20

var errListBudget = errors.New("Kubernetes list exceeds collection budget")

// A fresh paginated snapshot uses RV="", not RV=0: older API servers may
// ignore Limit for RV=0. A failed/expired page aborts the whole collection.
func visitPages[T runtime.Object](ctx context.Context, call func(context.Context, metav1.ListOptions) (runtime.Object, error), visit func(T) error) error {
	options := metav1.ListOptions{Limit: listPageSize}
	snapshotRV := ""
	seen := map[string]bool{}
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := call(ctx, options)
		if err != nil {
			return mapConnectionError(err)
		}
		length := meta.LenList(page)
		if length > listPageSize || total+length > listMaxObjects {
			return newError(CodeCollectionLimitExceeded)
		}
		total += length
		if err = meta.EachListItem(page, func(object runtime.Object) error {
			item, ok := object.(T)
			if !ok {
				return errors.New("unexpected Kubernetes list item")
			}
			return visit(item)
		}); err != nil {
			return err
		}
		accessor, err := meta.ListAccessor(page)
		if err != nil {
			return err
		}
		rv := accessor.GetResourceVersion()
		if snapshotRV != "" && rv != snapshotRV {
			return newError(CodeConnectionFailed)
		}
		snapshotRV = rv
		next := accessor.GetContinue()
		if next == "" {
			return nil
		}
		if len(next) > 16384 || seen[next] || len(seen) >= listMaxObjects/listPageSize {
			return newError(CodeCollectionLimitExceeded)
		}
		seen[next] = true
		options.Continue = next
	}
}

type listBudgetTransport struct{ base http.RoundTripper }

func (t listBudgetTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if err != nil || response == nil {
		return response, err
	}
	if r.Method == http.MethodGet && r.URL.Query().Get("limit") != "" && response.Body != nil {
		if response.ContentLength > listPageBytes {
			response.Body.Close()
			return nil, errListBudget
		}
		response.Body = &listBudgetBody{ReadCloser: response.Body, remaining: listPageBytes}
	}
	return response, nil
}

type listBudgetBody struct {
	io.ReadCloser
	remaining int64
}

func (body *listBudgetBody) Read(data []byte) (int, error) {
	if int64(len(data)) > body.remaining+1 {
		data = data[:body.remaining+1]
	}
	n, err := body.ReadCloser.Read(data)
	body.remaining -= int64(n)
	if body.remaining < 0 {
		return 0, errListBudget
	}
	return n, err
}

func visitPods(ctx context.Context, client kubernetes.Interface, namespace string, visit func(*corev1.Pod) error) error {
	return visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		return client.CoreV1().Pods(namespace).List(ctx, options)
	}, visit)
}

func visitReplicaSets(ctx context.Context, client kubernetes.Interface, namespace string, visit func(*appsv1.ReplicaSet) error) error {
	return visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		return client.AppsV1().ReplicaSets(namespace).List(ctx, options)
	}, visit)
}

func visitDeployments(ctx context.Context, client kubernetes.Interface, namespace string, visit func(*appsv1.Deployment) error) error {
	return visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		return client.AppsV1().Deployments(namespace).List(ctx, options)
	}, visit)
}

func visitStatefulSets(ctx context.Context, client kubernetes.Interface, namespace string, visit func(*appsv1.StatefulSet) error) error {
	return visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		return client.AppsV1().StatefulSets(namespace).List(ctx, options)
	}, visit)
}

func visitDaemonSets(ctx context.Context, client kubernetes.Interface, namespace string, visit func(*appsv1.DaemonSet) error) error {
	return visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		return client.AppsV1().DaemonSets(namespace).List(ctx, options)
	}, visit)
}

func visitNodes(ctx context.Context, client kubernetes.Interface, visit func(*corev1.Node) error) error {
	return visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		return client.CoreV1().Nodes().List(ctx, options)
	}, visit)
}

func visitNamespaces(ctx context.Context, client kubernetes.Interface, visit func(*corev1.Namespace) error) error {
	return visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		return client.CoreV1().Namespaces().List(ctx, options)
	}, visit)
}

func boundedServices(ctx context.Context, client kubernetes.Interface) (*corev1.ServiceList, error) {
	result := &corev1.ServiceList{}
	err := visitPages(ctx, func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		return client.CoreV1().Services("").List(ctx, options)
	}, func(item *corev1.Service) error {
		result.Items = append(result.Items, corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: item.Name, Namespace: item.Namespace, Labels: item.Labels}, Spec: corev1.ServiceSpec{Ports: item.Spec.Ports}})
		return nil
	})
	return result, err
}
