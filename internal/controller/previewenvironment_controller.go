package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	previewv1 "github.com/Garvity/preview-operator/api/v1alpha1"
)

const cleanupInterval = 2 * time.Second

var transitions = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "preview_operator_phase_transitions_total",
	Help: "Observed preview lifecycle phase transitions (not a count of unique previews).",
}, []string{"phase"})

func init() { metrics.Registry.MustRegister(transitions) }

// OwnershipConflict prevents accidental adoption or deletion of unrelated objects.
type OwnershipConflict struct{ Kind, Name string }

func (e *OwnershipConflict) Error() string {
	return fmt.Sprintf("%s %q exists but is not controlled by this PreviewEnvironment", e.Kind, e.Name)
}

// PreviewEnvironmentReconciler converges children and derives expiry from persisted metadata.
type PreviewEnvironmentReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	// Now is injectable for deterministic deadline and restart tests.
	Now func() time.Time
}

func (r *PreviewEnvironmentReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *PreviewEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	p := &previewv1.PreviewEnvironment{}
	if err := r.Get(ctx, req.NamespacedName, p); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !p.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	// API-server-created resources always have these fields. Fail closed for malformed fixtures/data.
	if p.UID == "" || p.CreationTimestamp.IsZero() {
		return ctrl.Result{}, fmt.Errorf("preview is missing server-assigned UID or creation timestamp")
	}
	remaining := p.Deadline().Sub(r.now())
	if remaining <= 0 {
		return r.expire(ctx, p)
	}

	// Preflight both names before mutating either resource.
	for _, obj := range []client.Object{&appsv1.Deployment{}, &corev1.Service{}} {
		if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
			if !apierrors.IsNotFound(err) {
				return r.failure(ctx, p, err, remaining)
			}
		} else if err := requireOwnership(p, obj); err != nil {
			return r.failure(ctx, p, err, remaining)
		}
	}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		if deployment.ResourceVersion != "" {
			if err := requireOwnership(p, deployment); err != nil {
				return err
			}
			if !deployment.DeletionTimestamp.IsZero() {
				return fmt.Errorf("Deployment %q is terminating", p.Name)
			}
		}
		if err := controllerutil.SetControllerReference(p, deployment, r.Scheme); err != nil {
			return err
		}
		configureDeployment(p, deployment)
		return nil
	})
	if err != nil {
		return r.failure(ctx, p, err, remaining)
	}
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
		if service.ResourceVersion != "" {
			if err := requireOwnership(p, service); err != nil {
				return err
			}
			if !service.DeletionTimestamp.IsZero() {
				return fmt.Errorf("Service %q is terminating", p.Name)
			}
		}
		if err := controllerutil.SetControllerReference(p, service, r.Scheme); err != nil {
			return err
		}
		configureService(p, service)
		return nil
	})
	if err != nil {
		return r.failure(ctx, p, err, remaining)
	}

	phase, reason, message := "Pending", "Progressing", "Waiting for the Deployment rollout"
	ready := deployment.Status.ObservedGeneration >= deployment.Generation &&
		deployment.Status.UpdatedReplicas == p.DesiredReplicas() &&
		deployment.Status.AvailableReplicas == p.DesiredReplicas() &&
		deployment.Status.Replicas == p.DesiredReplicas()
	if p.DesiredReplicas() == 0 {
		phase, reason, message, ready = "Paused", "ScaledToZero", "Application is scaled to zero; the expiry deadline is unchanged", false
	} else if ready {
		phase, reason, message = "Ready", "DeploymentAvailable", "All requested replicas are updated and available"
	} else {
		for _, c := range deployment.Status.Conditions {
			if c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionFalse {
				phase, reason, message = "Error", "RolloutFailed", c.Message
			}
		}
	}
	endpoint := ""
	if ready {
		endpoint = fmt.Sprintf("http://%s.%s.svc:%d", p.Name, p.Namespace, service.Spec.Ports[0].Port)
	}
	if err := r.updateStatus(ctx, p, phase, reason, message, endpoint, deployment.Status.AvailableReplicas, ready, false); err != nil {
		return ctrl.Result{}, err
	}
	// Watches provide immediate updates; a bounded resync also detects missed events.
	return ctrl.Result{RequeueAfter: min(remaining, 30*time.Second)}, nil
}

func requireOwnership(p *previewv1.PreviewEnvironment, obj client.Object) error {
	owner := metav1.GetControllerOf(obj)
	if owner == nil || owner.UID != p.UID || owner.Name != p.Name || owner.Kind != "PreviewEnvironment" || owner.APIVersion != previewv1.GroupVersion.String() {
		return &OwnershipConflict{Kind: fmt.Sprintf("%T", obj), Name: obj.GetName()}
	}
	return nil
}

func (r *PreviewEnvironmentReconciler) expire(ctx context.Context, p *previewv1.PreviewEnvironment) (ctrl.Result, error) {
	pending := false
	var cleanupErr error
	for _, obj := range []client.Object{&appsv1.Deployment{}, &corev1.Service{}} {
		if err := r.Get(ctx, client.ObjectKeyFromObject(p), obj); err != nil {
			if !apierrors.IsNotFound(err) {
				cleanupErr = errors.Join(cleanupErr, err)
			}
			continue
		}
		if err := requireOwnership(p, obj); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		pending = true
		if obj.GetDeletionTimestamp().IsZero() {
			uid := obj.GetUID()
			// Foreground deletion keeps the Deployment until its ReplicaSets/Pods are gone.
			err := r.Delete(ctx, obj, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}, PropagationPolicy: ptr(metav1.DeletePropagationForeground)})
			if err != nil && !apierrors.IsNotFound(err) {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		}
	}
	if cleanupErr != nil {
		return r.failure(ctx, p, cleanupErr, 0)
	}
	phase, reason, message := "Expired", "CleanupComplete", "Deadline elapsed and all managed resources have been removed"
	if pending {
		phase, reason, message = "Expiring", "CleanupInProgress", "Deadline elapsed; waiting for managed resources to be deleted"
	}
	if err := r.updateStatus(ctx, p, phase, reason, message, "", 0, false, true); err != nil {
		return ctrl.Result{}, err
	}
	if pending {
		return ctrl.Result{RequeueAfter: cleanupInterval}, nil
	}
	return ctrl.Result{}, nil
}

func (r *PreviewEnvironmentReconciler) failure(ctx context.Context, p *previewv1.PreviewEnvironment, cause error, remaining time.Duration) (ctrl.Result, error) {
	phase, reason := "Error", "ReconcileFailed"
	var conflict *OwnershipConflict
	if errors.As(cause, &conflict) {
		phase, reason = "Conflict", "OwnershipConflict"
	}
	err := r.updateStatus(ctx, p, phase, reason, cause.Error(), "", 0, false, remaining <= 0)
	if err != nil {
		return ctrl.Result{}, errors.Join(cause, err)
	}
	if conflict != nil {
		// Unowned resources are not watched, so poll for their removal without a hot loop.
		delay := 15 * time.Second
		if remaining > 0 {
			delay = min(delay, remaining)
		}
		return ctrl.Result{RequeueAfter: delay}, nil
	}
	return ctrl.Result{}, cause
}

func (r *PreviewEnvironmentReconciler) updateStatus(ctx context.Context, p *previewv1.PreviewEnvironment, phase, reason, message, endpoint string, available int32, ready, expired bool) error {
	before := p.DeepCopy()
	deadline := metav1.NewTime(p.Deadline())
	p.Status.ObservedGeneration, p.Status.ExpiresAt = p.Generation, &deadline
	p.Status.Phase, p.Status.Endpoint, p.Status.AvailableReplicas = phase, endpoint, available
	for _, c := range []struct {
		kind            string
		value           bool
		reason, message string
	}{
		{"Ready", ready, reason, message},
		{"Expired", expired, map[bool]string{true: "DeadlineElapsed", false: "WithinLifetime"}[expired], "Expiry is measured from creationTimestamp plus ttlSeconds"},
	} {
		status := metav1.ConditionFalse
		if c.value {
			status = metav1.ConditionTrue
		}
		meta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{Type: c.kind, Status: status, Reason: c.reason, Message: c.message, ObservedGeneration: p.Generation, LastTransitionTime: metav1.NewTime(r.now())})
	}
	if reflect.DeepEqual(before.Status, p.Status) {
		return nil
	}
	if err := r.Status().Patch(ctx, p, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return err
	}
	if before.Status.Phase != phase {
		transitions.WithLabelValues(phase).Inc()
		log.FromContext(ctx).Info("Preview phase changed", "phase", phase, "reason", reason, "expiresAt", deadline)
		if r.Recorder != nil {
			eventType := corev1.EventTypeNormal
			if phase == "Conflict" || phase == "Error" {
				eventType = corev1.EventTypeWarning
			}
			r.Recorder.Event(p, eventType, reason, message)
		}
	}
	return nil
}

func (r *PreviewEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&previewv1.PreviewEnvironment{}).
		Owns(&appsv1.Deployment{}).Owns(&corev1.Service{}).Complete(r)
}

func ptr[T any](v T) *T { return &v }
