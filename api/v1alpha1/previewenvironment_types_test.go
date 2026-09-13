package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

func TestDeadlineSurvivesRestartAndIgnoresStatus(t *testing.T) {
	created := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	p := &PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(created)}, Spec: PreviewEnvironmentSpec{TTLSeconds: 90}}
	p.Status.ExpiresAt = &metav1.Time{Time: created.Add(24 * time.Hour)}
	if got := p.DeepCopy().Deadline(); !got.Equal(created.Add(90 * time.Second)) {
		t.Fatalf("deadline = %s", got)
	}
}

func TestDefaultsAndExplicitScaleToZero(t *testing.T) {
	p := &PreviewEnvironment{}
	if p.DesiredReplicas() != 1 {
		t.Fatal("default replicas must be one")
	}
	zero := int32(0)
	p.Spec.Replicas = &zero
	if p.DesiredReplicas() != 0 {
		t.Fatal("explicit zero must be preserved")
	}
	if !p.Deadline().Equal(p.CreationTimestamp.Add(time.Hour)) {
		t.Fatal("default TTL must be one hour")
	}
}
