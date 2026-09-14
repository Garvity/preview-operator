package controller

import (
	previewv1 "github.com/Garvity/preview-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const ownerLabel = "preview.garv.dev/environment-uid"

func labels(p *previewv1.PreviewEnvironment) map[string]string {
	return map[string]string{ownerLabel: string(p.UID), "app.kubernetes.io/managed-by": "preview-operator", "app.kubernetes.io/name": p.Name}
}

func mergeLabels(existing, managed map[string]string) map[string]string {
	if existing == nil {
		existing = map[string]string{}
	}
	for k, v := range managed {
		existing[k] = v
	}
	return existing
}

func configureDeployment(p *previewv1.PreviewEnvironment, d *appsv1.Deployment) {
	port := p.Spec.ContainerPort
	if port == 0 {
		port = 8080
	}
	resources := *p.Spec.Resources.DeepCopy()
	if len(resources.Requests) == 0 && len(resources.Limits) == 0 {
		resources.Requests = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("32Mi")}
		resources.Limits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("128Mi")}
	}
	d.Labels = mergeLabels(d.Labels, labels(p))
	d.Spec.Replicas = ptr(p.DesiredReplicas())
	d.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{ownerLabel: string(p.UID)}}
	d.Spec.RevisionHistoryLimit = ptr(int32(2))
	d.Spec.ProgressDeadlineSeconds = ptr(int32(120))
	d.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType, RollingUpdate: &appsv1.RollingUpdateDeployment{MaxSurge: ptr(intstr.FromInt32(1)), MaxUnavailable: ptr(intstr.FromInt32(0))}}
	d.Spec.Template = corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels(p)},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyAlways, DNSPolicy: corev1.DNSClusterFirst, SchedulerName: corev1.DefaultSchedulerName,
			TerminationGracePeriodSeconds: ptr(int64(30)), AutomountServiceAccountToken: ptr(false), EnableServiceLinks: ptr(false),
			SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: ptr(true), RunAsUser: ptr(int64(10001)), RunAsGroup: ptr(int64(10001)), FSGroup: ptr(int64(10001)), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			Volumes:         []corev1.Volume{{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptr(resource.MustParse("64Mi"))}}}},
			Containers: []corev1.Container{{
				Name: "app", Image: p.Spec.Image, Args: append([]string(nil), p.Spec.Args...), ImagePullPolicy: corev1.PullIfNotPresent,
				Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: port, Protocol: corev1.ProtocolTCP}}, Resources: resources,
				TerminationMessagePath: corev1.TerminationMessagePathDefault, TerminationMessagePolicy: corev1.TerminationMessageReadFile,
				SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: ptr(false), ReadOnlyRootFilesystem: ptr(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
				ReadinessProbe:  &corev1.Probe{ProbeHandler: corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("http")}}, PeriodSeconds: 5, TimeoutSeconds: 1, SuccessThreshold: 1, FailureThreshold: 3},
				VolumeMounts:    []corev1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}},
			}},
		},
	}
}

func configureService(p *previewv1.PreviewEnvironment, s *corev1.Service) {
	port := p.Spec.ServicePort
	if port == 0 {
		port = 80
	}
	s.Labels = mergeLabels(s.Labels, labels(p))
	// Preserve API-server-allocated ClusterIP/IP families while correcting managed fields.
	s.Spec.Type = corev1.ServiceTypeClusterIP
	s.Spec.Selector = map[string]string{ownerLabel: string(p.UID)}
	s.Spec.Ports = []corev1.ServicePort{{Name: "http", Port: port, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP}}
	s.Spec.SessionAffinity = corev1.ServiceAffinityNone
	s.Spec.InternalTrafficPolicy = ptr(corev1.ServiceInternalTrafficPolicyCluster)
	s.Spec.ExternalIPs = nil
	s.Spec.LoadBalancerSourceRanges = nil
	s.Spec.ExternalName = ""
	s.Spec.PublishNotReadyAddresses = false
}
