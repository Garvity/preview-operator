package v1alpha1

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PreviewEnvironmentSpec describes one temporary application.
type PreviewEnvironmentSpec struct {
	// Image is an OCI container image reference. Use a digest for reproducibility.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^\S+$`
	Image string `json:"image"`
	// Args overrides the container image's default arguments.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	Args []string `json:"args,omitempty"`
	// Replicas may be zero to pause the application without extending its deadline.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10
	Replicas *int32 `json:"replicas,omitempty"`
	// ContainerPort is the application's TCP listening port.
	// +optional
	// +kubebuilder:default=8080
	// +kubebuilder:validation:Minimum=1024
	// +kubebuilder:validation:Maximum=65535
	ContainerPort int32 `json:"containerPort,omitempty"`
	// ServicePort is the port exposed by the ClusterIP Service.
	// +optional
	// +kubebuilder:default=80
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ServicePort int32 `json:"servicePort,omitempty"`
	// TTLSeconds is immutable and measured from metadata.creationTimestamp.
	// +optional
	// +kubebuilder:default=3600
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=604800
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ttlSeconds is immutable; create a new environment for a new lifetime"
	TTLSeconds int32 `json:"ttlSeconds,omitempty"`
	// Resources configures requests and limits. Omission uses conservative defaults.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// PreviewEnvironmentStatus records observed state; it is not the expiry clock.
type PreviewEnvironmentStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	// +kubebuilder:validation:Enum=Pending;Ready;Paused;Expiring;Expired;Conflict;Error
	Phase string `json:"phase,omitempty"`
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
	// Endpoint is a cluster-local HTTP address, published only while Ready.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`
	// +optional
	AvailableReplicas int32 `json:"availableReplicas,omitempty"`
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PreviewEnvironment provisions a temporary Deployment and Service in its namespace.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=preview;pe
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.status.expiresAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="self.metadata.name.size() <= 63",message="name must be at most 63 characters"
// +kubebuilder:validation:XValidation:rule="self.metadata.name.matches('^[a-z]([-a-z0-9]*[a-z0-9])?$')",message="name must be a DNS label starting with a letter"
type PreviewEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PreviewEnvironmentSpec `json:"spec"`
	// +optional
	Status PreviewEnvironmentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type PreviewEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PreviewEnvironment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PreviewEnvironment{}, &PreviewEnvironmentList{})
}

// Deadline derives a restart-safe absolute deadline from API server metadata.
func (p *PreviewEnvironment) Deadline() time.Time {
	ttl := p.Spec.TTLSeconds
	if ttl == 0 {
		ttl = 3600
	}
	return p.CreationTimestamp.Add(time.Duration(ttl) * time.Second)
}

func (p *PreviewEnvironment) DesiredReplicas() int32 {
	if p.Spec.Replicas == nil {
		return 1
	}
	return *p.Spec.Replicas
}
