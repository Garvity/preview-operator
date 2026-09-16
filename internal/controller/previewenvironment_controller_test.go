package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	previewv1 "github.com/Garvity/preview-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var testNow = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

func newFixture(t *testing.T, objects ...client.Object) (*PreviewEnvironmentReconciler, *previewv1.PreviewEnvironment) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, appsv1.AddToScheme, previewv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	p := &previewv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "test", UID: types.UID("e71713ea-9abc-4dd9-a1d0-dcaac7058712"), Generation: 1, CreationTimestamp: metav1.NewTime(testNow)}, Spec: previewv1.PreviewEnvironmentSpec{Image: "example.invalid/app:v1", TTLSeconds: 60}}
	objects = append(objects, p)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&previewv1.PreviewEnvironment{}, &appsv1.Deployment{}).WithObjects(objects...).Build()
	return &PreviewEnvironmentReconciler{Client: c, Scheme: scheme, Now: func() time.Time { return testNow }}, p
}

func reconcile(t *testing.T, r *PreviewEnvironmentReconciler, p *previewv1.PreviewEnvironment) ctrl.Result {
	t.Helper()
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func getPreview(t *testing.T, r *PreviewEnvironmentReconciler, p *previewv1.PreviewEnvironment) *previewv1.PreviewEnvironment {
	t.Helper()
	got := &previewv1.PreviewEnvironment{}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(p), got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestProvisionRepairAndIdempotency(t *testing.T) {
	r, p := newFixture(t)
	ctx, key := context.Background(), client.ObjectKeyFromObject(p)
	result := reconcile(t, r, p)
	if result.RequeueAfter <= 0 || result.RequeueAfter > time.Minute {
		t.Fatal("must requeue by expiry")
	}
	d, s := &appsv1.Deployment{}, &corev1.Service{}
	if err := r.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, key, s); err != nil {
		t.Fatal(err)
	}
	if !metav1.IsControlledBy(d, p) || !metav1.IsControlledBy(s, p) {
		t.Fatal("missing owner references")
	}
	container := d.Spec.Template.Spec.Containers[0]
	if !*container.SecurityContext.ReadOnlyRootFilesystem || *d.Spec.Template.Spec.AutomountServiceAccountToken {
		t.Fatal("unsafe pod defaults")
	}
	if s.Spec.Selector[ownerLabel] != string(p.UID) {
		t.Fatal("service selector must be UID scoped")
	}
	beforeD, beforeS, beforeP := d.ResourceVersion, s.ResourceVersion, getPreview(t, r, p).ResourceVersion
	reconcile(t, r, p)
	_ = r.Get(ctx, key, d)
	_ = r.Get(ctx, key, s)
	if d.ResourceVersion != beforeD || s.ResourceVersion != beforeS || getPreview(t, r, p).ResourceVersion != beforeP {
		t.Fatal("unchanged reconciliation must not write")
	}
	d.Spec.Template.Spec.Containers[0].Image = "unwanted:v2"
	d.Spec.Paused = true
	d.Spec.MinReadySeconds = 30
	if err := r.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, s); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p)
	_ = r.Get(ctx, key, d)
	if d.Spec.Template.Spec.Containers[0].Image != p.Spec.Image {
		t.Fatal("image drift was not repaired")
	}
	if d.Spec.Paused || d.Spec.MinReadySeconds != 0 {
		t.Fatal("rollout policy drift was not repaired")
	}
	if err := r.Get(ctx, key, s); err != nil {
		t.Fatal("service was not recreated", err)
	}
	if err := r.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p)
	if err := r.Get(ctx, key, d); err != nil {
		t.Fatal("deployment was not recreated", err)
	}
}

func TestReadinessRequiresCurrentRolloutAndScaleToZero(t *testing.T) {
	r, p := newFixture(t)
	ctx, key := context.Background(), client.ObjectKeyFromObject(p)
	reconcile(t, r, p)
	d := &appsv1.Deployment{}
	_ = r.Get(ctx, key, d)
	d.Generation = 2
	if err := r.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	d.Status = appsv1.DeploymentStatus{ObservedGeneration: 1, AvailableReplicas: 1, UpdatedReplicas: 1, Replicas: 1}
	if err := r.Status().Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p)
	if getPreview(t, r, p).Status.Phase != "Pending" {
		t.Fatal("stale rollout must not be ready")
	}
	d.Status.ObservedGeneration = 2
	if err := r.Status().Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p)
	got := getPreview(t, r, p)
	if got.Status.Phase != "Ready" || got.Status.Endpoint != "http://demo.test.svc:80" {
		t.Fatalf("unexpected status: %+v", got.Status)
	}
	got.Spec.Replicas = ptr(int32(0))
	if err := r.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p)
	if got = getPreview(t, r, p); got.Status.Phase != "Paused" || got.Status.Endpoint != "" {
		t.Fatal("scale to zero must clear readiness")
	}
}

func TestConflictsAreNeverAdoptedOrDeleted(t *testing.T) {
	for _, kind := range []string{"deployment", "service"} {
		t.Run(kind, func(t *testing.T) {
			var obj client.Object = &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "test", UID: "foreign"}}
			if kind == "service" {
				obj = &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "test", UID: "foreign"}}
			}
			r, p := newFixture(t, obj)
			reconcile(t, r, p)
			if getPreview(t, r, p).Status.Phase != "Conflict" {
				t.Fatal("must expose ownership conflict")
			}
			r.Now = func() time.Time { return testNow.Add(time.Minute) }
			reconcile(t, r, p)
			if err := r.Get(context.Background(), client.ObjectKeyFromObject(p), obj); err != nil {
				t.Fatal("foreign resource removed", err)
			}
			if len(obj.GetOwnerReferences()) != 0 || obj.GetUID() != "foreign" {
				t.Fatal("foreign resource mutated")
			}
		})
	}
}

func TestExpiryAfterRestartAndNoResurrection(t *testing.T) {
	r, p := newFixture(t)
	reconcile(t, r, p)
	// A fresh reconciler has no in-memory state and still recognizes the deadline.
	r = &PreviewEnvironmentReconciler{Client: r.Client, Scheme: r.Scheme, Now: func() time.Time { return testNow.Add(time.Minute) }}
	reconcile(t, r, p)
	reconcile(t, r, p)
	if got := getPreview(t, r, p); got.Status.Phase != "Expired" || got.Status.Endpoint != "" {
		t.Fatalf("status=%+v", got.Status)
	}
	reconcile(t, r, p)
	for _, obj := range []client.Object{&appsv1.Deployment{}, &corev1.Service{}} {
		if err := r.Get(context.Background(), client.ObjectKeyFromObject(p), obj); !apierrors.IsNotFound(err) {
			t.Fatalf("expired child exists: %v", err)
		}
	}
}

func TestExpiryWaitsForDeletionAndUsesUIDPreconditions(t *testing.T) {
	r, p := newFixture(t)
	reconcile(t, r, p)
	d := &appsv1.Deployment{}
	ctx, key := context.Background(), client.ObjectKeyFromObject(p)
	_ = r.Get(ctx, key, d)
	d.UID = "deployment-uid"
	d.Finalizers = []string{"test.garv.dev/hold"}
	if err := r.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	deleted := false
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
		if _, ok := obj.(*appsv1.Deployment); ok {
			options := (&client.DeleteOptions{}).ApplyOptions(opts)
			if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != "deployment-uid" {
				t.Fatal("missing UID precondition")
			}
			if options.PropagationPolicy == nil || *options.PropagationPolicy != metav1.DeletePropagationForeground {
				t.Fatal("must wait for dependent pods")
			}
			deleted = true
		}
		return c.Delete(ctx, obj, opts...)
	}})
	r.Now = func() time.Time { return testNow.Add(time.Minute) }
	result := reconcile(t, r, p)
	if !deleted || result.RequeueAfter != cleanupInterval || getPreview(t, r, p).Status.Phase != "Expiring" {
		t.Fatal("must wait for deletion")
	}
}

func TestTransientWriteFailureIsReportedAndRetried(t *testing.T) {
	r, p := newFixture(t)
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
		return errors.New("API temporarily unavailable")
	}})
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
	if err == nil {
		t.Fatal("transient errors must trigger rate-limited retry")
	}
	if getPreview(t, r, p).Status.Phase != "Error" {
		t.Fatal("status must explain the failure")
	}
}

func TestMissingPreviewIsNoop(t *testing.T) {
	r, _ := newFixture(t)
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "missing", Namespace: "test"}}); err != nil {
		t.Fatal(err)
	}
}
