//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	previewv1 "github.com/Garvity/preview-operator/api/v1alpha1"
	"github.com/Garvity/preview-operator/internal/controller"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestRealAPILifecycle(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Fatal("set KUBEBUILDER_ASSETS via make envtest test-integration")
	}
	testEnv := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}, ErrorIfCRDPathMissing: true}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, appsv1.AddToScheme, previewv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "integration"}}); err != nil {
		t.Fatal(err)
	}
	newPreview := func(name string) *previewv1.PreviewEnvironment {
		return &previewv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "integration"}, Spec: previewv1.PreviewEnvironmentSpec{Image: "hashicorp/http-echo:1.0.0"}}
	}

	t.Run("schema-validation", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			modify func(*previewv1.PreviewEnvironment)
		}{
			{"empty-image", func(p *previewv1.PreviewEnvironment) { p.Spec.Image = "" }},
			{"short-ttl", func(p *previewv1.PreviewEnvironment) { p.Spec.TTLSeconds = 1 }},
			{"long-ttl", func(p *previewv1.PreviewEnvironment) { p.Spec.TTLSeconds = 604801 }},
			{"privileged-port", func(p *previewv1.PreviewEnvironment) { p.Spec.ContainerPort = 80 }},
			{"too-many-replicas", func(p *previewv1.PreviewEnvironment) { v := int32(11); p.Spec.Replicas = &v }},
			{"long-name", func(p *previewv1.PreviewEnvironment) { p.Name = strings.Repeat("a", 64) }},
			{"dot-name", func(p *previewv1.PreviewEnvironment) { p.Name = "has.dot" }},
			{"numeric-name", func(p *previewv1.PreviewEnvironment) { p.Name = "1preview" }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				p := newPreview(tc.name)
				tc.modify(p)
				if err := c.Create(ctx, p); !apierrors.IsInvalid(err) {
					t.Fatalf("expected admission rejection, got %v", err)
				}
			})
		}
	})

	p := newPreview("lifecycle")
	if err := c.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKeyFromObject(p)
	if err := c.Get(ctx, key, p); err != nil {
		t.Fatal(err)
	}
	t.Run("defaults-and-immutable-ttl", func(t *testing.T) {
		if p.Spec.TTLSeconds != 3600 || p.Spec.ContainerPort != 8080 || p.Spec.ServicePort != 80 || p.DesiredReplicas() != 1 {
			t.Fatalf("defaults missing: %+v", p.Spec)
		}
		changed := p.DeepCopy()
		changed.Spec.TTLSeconds = 7200
		if err := c.Update(ctx, changed); !apierrors.IsInvalid(err) {
			t.Fatalf("TTL must be immutable: %v", err)
		}
	})
	r := &controller.PreviewEnvironmentReconciler{Client: c, Scheme: scheme}
	reconcile := func() {
		t.Helper()
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
	d, s := &appsv1.Deployment{}, &corev1.Service{}
	if err := c.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, key, s); err != nil {
		t.Fatal(err)
	}
	t.Run("defaults-do-not-cause-write-loops", func(t *testing.T) {
		if err := c.Get(ctx, key, p); err != nil {
			t.Fatal(err)
		}
		dv, sv, pv := d.ResourceVersion, s.ResourceVersion, p.ResourceVersion
		reconcile()
		_ = c.Get(ctx, key, d)
		_ = c.Get(ctx, key, s)
		_ = c.Get(ctx, key, p)
		if dv != d.ResourceVersion || sv != s.ResourceVersion || pv != p.ResourceVersion {
			t.Fatalf("resource versions changed: deployment %s/%s service %s/%s preview %s/%s", dv, d.ResourceVersion, sv, s.ResourceVersion, pv, p.ResourceVersion)
		}
	})
	t.Run("readiness-and-new-generation", func(t *testing.T) {
		// envtest has no Deployment controller; simulate only its status, not a real pod.
		d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1}
		if err := c.Status().Update(ctx, d); err != nil {
			t.Fatal(err)
		}
		reconcile()
		_ = c.Get(ctx, key, p)
		if p.Status.Phase != "Ready" {
			t.Fatal("expected Ready")
		}
		p.Spec.Image = "hashicorp/http-echo:latest"
		if err := c.Update(ctx, p); err != nil {
			t.Fatal(err)
		}
		reconcile()
		_ = c.Get(ctx, key, p)
		if p.Status.Phase != "Pending" || p.Status.Endpoint != "" {
			t.Fatal("old deployment status must not mark new rollout ready")
		}
	})
	t.Run("service-repair-and-allocated-ip", func(t *testing.T) {
		oldIP := s.Spec.ClusterIP
		p.Spec.ServicePort = 8088
		if err := c.Update(ctx, p); err != nil {
			t.Fatal(err)
		}
		reconcile()
		_ = c.Get(ctx, key, s)
		if s.Spec.ClusterIP != oldIP || s.Spec.Ports[0].Port != 8088 {
			t.Fatal("must preserve allocated IP while updating port")
		}
		if err := c.Delete(ctx, s); err != nil {
			t.Fatal(err)
		}
		reconcile()
		_ = c.Get(ctx, key, s)
		if !metav1.IsControlledBy(s, p) {
			t.Fatal("service was not recreated with ownership")
		}
	})
	t.Run("limits-only-budget-is-idempotent", func(t *testing.T) {
		if err := c.Get(ctx, key, p); err != nil {
			t.Fatal(err)
		}
		p.Spec.Resources = corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("128Mi")}}
		if err := c.Update(ctx, p); err != nil {
			t.Fatal(err)
		}
		reconcile()
		if err := c.Get(ctx, key, d); err != nil {
			t.Fatal(err)
		}
		version := d.ResourceVersion
		reconcile()
		if err := c.Get(ctx, key, d); err != nil {
			t.Fatal(err)
		}
		if d.ResourceVersion != version {
			t.Fatalf("limits-only budget causes repeated writes: %s -> %s", version, d.ResourceVersion)
		}
	})
	t.Run("deployment-pause-drift-is-repaired", func(t *testing.T) {
		if err := c.Get(ctx, key, d); err != nil {
			t.Fatal(err)
		}
		d.Spec.Paused = true
		d.Spec.MinReadySeconds = 30
		if err := c.Update(ctx, d); err != nil {
			t.Fatal(err)
		}
		reconcile()
		if err := c.Get(ctx, key, d); err != nil {
			t.Fatal(err)
		}
		if d.Spec.Paused || d.Spec.MinReadySeconds != 0 {
			t.Fatal("out-of-band rollout policy drift was not repaired")
		}
	})
	t.Run("expiry-waits-for-garbage-collection", func(t *testing.T) {
		r.Now = func() time.Time { return p.Deadline().Add(time.Second) }
		reconcile()
		_ = c.Get(ctx, key, p)
		if p.Status.Phase != "Expiring" {
			t.Fatalf("expected Expiring: %+v", p.Status)
		}
		_ = c.Get(ctx, key, d)
		if d.DeletionTimestamp.IsZero() {
			t.Fatal("deployment deletion not requested")
		}
		// envtest has no garbage collector. Completion is tested in kind.
	})
}
