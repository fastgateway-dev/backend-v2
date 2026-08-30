//go:build e2e

package harness

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// These exercise deploymentIsAvailable's pure logic directly -- no cluster
// required, so they can (and must) run before any cluster exists. See
// go test -tags e2e ./e2e/harness/ -run TestDeploymentIsAvailable -v.
//
// The scenario that motivates this: ScaleDeployment bumps a Deployment
// from 1 replica (already Available=True) to 3. The very next Get can
// still return the OLD generation's status -- Available=True,
// ReadyReplicas=1 -- before the controller has observed the new
// generation at all. Trusting the Available condition alone made
// WaitDeploymentAvailable return on that first, stale read.

func replicas(n int32) *int32 { return &n }

func TestDeploymentIsAvailable(t *testing.T) {
	availableCond := appsv1.DeploymentCondition{
		Type:   appsv1.DeploymentAvailable,
		Status: corev1.ConditionTrue,
	}
	notAvailableCond := appsv1.DeploymentCondition{
		Type:   appsv1.DeploymentAvailable,
		Status: corev1.ConditionFalse,
	}

	tests := []struct {
		name string
		dep  *appsv1.Deployment
		want bool
	}{
		{
			name: "stale Available=True from before a scale-up is rejected",
			dep: &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: replicas(3)},
				Status: appsv1.DeploymentStatus{
					// Controller has observed the new generation, but
					// ReadyReplicas/UpdatedReplicas still reflect the old
					// spec (1 replica) -- exactly the stale-Get scenario
					// from ScaleDeployment(1 -> 3).
					ObservedGeneration: 2,
					Conditions:         []appsv1.DeploymentCondition{availableCond},
					ReadyReplicas:      1,
					UpdatedReplicas:    1,
					Replicas:           3,
				},
			},
			want: false,
		},
		{
			name: "observed generation behind spec generation is rejected",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       appsv1.DeploymentSpec{Replicas: replicas(3)},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: 1,
					Conditions:         []appsv1.DeploymentCondition{availableCond},
					ReadyReplicas:      3,
					UpdatedReplicas:    3,
					Replicas:           3,
				},
			},
			want: false,
		},
		{
			name: "fully rolled out at 3 replicas is available",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       appsv1.DeploymentSpec{Replicas: replicas(3)},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: 2,
					Conditions:         []appsv1.DeploymentCondition{availableCond},
					ReadyReplicas:      3,
					UpdatedReplicas:    3,
					Replicas:           3,
				},
			},
			want: true,
		},
		{
			name: "ready but not yet updated (old-generation pods still ready) is rejected",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       appsv1.DeploymentSpec{Replicas: replicas(3)},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: 2,
					Conditions:         []appsv1.DeploymentCondition{availableCond},
					ReadyReplicas:      3,
					UpdatedReplicas:    2,
					Replicas:           3,
				},
			},
			want: false,
		},
		{
			name: "Available condition False is rejected even if replica counts match",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec:       appsv1.DeploymentSpec{Replicas: replicas(3)},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: 1,
					Conditions:         []appsv1.DeploymentCondition{notAvailableCond},
					ReadyReplicas:      3,
					UpdatedReplicas:    3,
					Replicas:           3,
				},
			},
			want: false,
		},
		{
			name: "no Available condition at all is rejected",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec:       appsv1.DeploymentSpec{Replicas: replicas(1)},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: 1,
					ReadyReplicas:      1,
					UpdatedReplicas:    1,
					Replicas:           1,
				},
			},
			want: false,
		},
		{
			name: "scaled to 0 replicas counts as available, so scale-down callers never hang",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 3},
				Spec:       appsv1.DeploymentSpec{Replicas: replicas(0)},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: 3,
					Conditions:         []appsv1.DeploymentCondition{availableCond},
					ReadyReplicas:      0,
					UpdatedReplicas:    0,
					Replicas:           0,
				},
			},
			want: true,
		},
		{
			name: "nil Spec.Replicas defaults to 1, matching Kubernetes' own defaulting",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec:       appsv1.DeploymentSpec{Replicas: nil},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: 1,
					Conditions:         []appsv1.DeploymentCondition{availableCond},
					ReadyReplicas:      1,
					UpdatedReplicas:    1,
					Replicas:           1,
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deploymentIsAvailable(tt.dep)
			if got != tt.want {
				t.Fatalf("deploymentIsAvailable() = %v, want %v", got, tt.want)
			}
		})
	}
}
