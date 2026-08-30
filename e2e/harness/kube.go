//go:build e2e

package harness

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Kube wraps a typed clientset and a dynamic client built from the ambient
// kubeconfig. Unlike the Python predecessor -- which pinned
// `--context=orbstack` in three places and disagreed with itself in two
// others -- the context is never hardcoded: KUBE_CONTEXT selects a
// context when set, and an empty value means "whatever the kubeconfig's
// current-context is".
type Kube struct {
	Clientset kubernetes.Interface
	Dynamic   dynamic.Interface
}

// NewKube builds a Kube client from the ambient kubeconfig (respecting
// $KUBECONFIG / ~/.kube/config via the standard loading rules), honouring
// cfg.KubeContext when set.
func NewKube(cfg *Config) (*Kube, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if cfg.KubeContext != "" {
		overrides.CurrentContext = cfg.KubeContext
	}
	kubeCfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)

	restCfg, err := kubeCfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}

	return &Kube{Clientset: clientset, Dynamic: dyn}, nil
}

// ScaleDeployment sets a Deployment's replica count.
func (k *Kube) ScaleDeployment(ctx context.Context, ns, name string, replicas int32) error {
	scale, err := k.Clientset.AppsV1().Deployments(ns).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get scale for deployment %s/%s: %w", ns, name, err)
	}
	scale.Spec.Replicas = replicas
	if _, err := k.Clientset.AppsV1().Deployments(ns).UpdateScale(ctx, name, scale, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update scale for deployment %s/%s to %d: %w", ns, name, replicas, err)
	}
	return nil
}

// PodLogs returns the concatenated tail of logs from every pod matching
// selector in ns, each section prefixed with the pod's name.
func (k *Kube) PodLogs(ctx context.Context, ns, selector string, tailLines int64) (string, error) {
	pods, err := k.Clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return "", fmt.Errorf("list pods in %s matching %q: %w", ns, selector, err)
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found in %s matching selector %q", ns, selector)
	}

	var buf bytes.Buffer
	for i, pod := range pods.Items {
		if i > 0 {
			buf.WriteString("\n---\n")
		}
		fmt.Fprintf(&buf, "== %s ==\n", pod.Name)

		stream, err := k.Clientset.CoreV1().Pods(ns).GetLogs(pod.Name, &corev1.PodLogOptions{TailLines: &tailLines}).Stream(ctx)
		if err != nil {
			fmt.Fprintf(&buf, "error fetching logs: %v\n", err)
			continue
		}
		b, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			fmt.Fprintf(&buf, "error reading logs: %v\n", err)
			continue
		}
		buf.Write(b)
	}
	return buf.String(), nil
}

// GetUnstructured fetches an arbitrary resource by GVR/namespace/name
// (namespace "" for cluster-scoped resources) via the dynamic client.
func (k *Kube) GetUnstructured(ctx context.Context, gvr schema.GroupVersionResource, ns, name string) (*unstructured.Unstructured, error) {
	ri := k.Dynamic.Resource(gvr)
	var getter dynamic.ResourceInterface = ri
	if ns != "" {
		getter = ri.Namespace(ns)
	}
	obj, err := getter.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get %s %s/%s: %w", gvr.Resource, ns, name, err)
	}
	return obj, nil
}

// GetUnstructuredByLabel fetches the single resource matching GVR/namespace
// (namespace "" for cluster-scoped resources) that carries labelSelector
// (e.g. "fastgateway.dev/route-id=<uuid>") via the dynamic client's List.
//
// This exists because the backend does not always expose the Kubernetes
// object name it generates: e.g. models.Route.K8sRouteName (the actual
// HTTPRoute name, "<name>-<8 hex chars of the route UUID>") is tagged
// `json:"-"` and never serialized, so callers cannot look the object up
// by name at all. Every backend-generated object is labeled with the
// owning entity's ID instead, so resolving by label is the only route
// available to a black-box e2e caller. It errors if zero or more than one
// object matches, since callers rely on that ID being unique per object.
func (k *Kube) GetUnstructuredByLabel(ctx context.Context, gvr schema.GroupVersionResource, ns, labelSelector string) (*unstructured.Unstructured, error) {
	ri := k.Dynamic.Resource(gvr)
	var lister dynamic.ResourceInterface = ri
	if ns != "" {
		lister = ri.Namespace(ns)
	}
	list, err := lister.List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, fmt.Errorf("list %s in %s matching %q: %w", gvr.Resource, ns, labelSelector, err)
	}
	switch len(list.Items) {
	case 0:
		return nil, fmt.Errorf("no %s found in %s matching %q", gvr.Resource, ns, labelSelector)
	case 1:
		return &list.Items[0], nil
	default:
		return nil, fmt.Errorf("%d %s found in %s matching %q, want exactly 1", len(list.Items), gvr.Resource, ns, labelSelector)
	}
}

// deploymentIsAvailable reports whether dep has genuinely finished rolling
// out to its current spec, rather than merely carrying a stale
// Available=True condition left over from before the most recent scale or
// update. A Get issued right after e.g. ScaleDeployment can return the
// previous generation's status -- Available=True at the old replica count
// -- so the "Available" condition alone is not sufficient: the controller
// must also have observed the current generation, and driven both
// ReadyReplicas and UpdatedReplicas to the desired replica count.
//
// A Deployment intentionally scaled to 0 is still correctly reported
// available: desired falls out to 0, and ReadyReplicas/UpdatedReplicas are
// 0 too, so a caller scaling down never hangs waiting for a replica count
// that will never arrive. A nil Spec.Replicas (as the API server would
// never actually return, but a hand-built Deployment in a test might)
// defaults to 1, matching Kubernetes' own defaulting convention.
func deploymentIsAvailable(dep *appsv1.Deployment) bool {
	if dep.Status.ObservedGeneration < dep.Generation {
		return false
	}

	available := false
	for _, cond := range dep.Status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
			available = true
			break
		}
	}
	if !available {
		return false
	}

	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}

	return dep.Status.ReadyReplicas == desired && dep.Status.UpdatedReplicas == desired
}

// WaitDeploymentAvailable polls until the named Deployment has fully rolled
// out -- see deploymentIsAvailable -- or returns an error once timeout
// elapses.
func (k *Kube) WaitDeploymentAvailable(ctx context.Context, ns, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		dep, err := k.Clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			lastErr = err
		} else if deploymentIsAvailable(dep) {
			return nil
		} else {
			lastErr = fmt.Errorf("deployment %s/%s not available yet (%d/%d replicas ready)",
				ns, name, dep.Status.ReadyReplicas, dep.Status.Replicas)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("deployment %s/%s not available within %s: %w", ns, name, timeout, lastErr)
}
