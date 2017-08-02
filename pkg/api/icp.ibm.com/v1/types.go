package v1

import (
	autoscaling "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ActionSpec is to define the action of the policy
type ActionSpec string

const (
	// ScaleUp action
	ScaleUp ActionSpec = "scaleUp"
	// ScaleDown action
	ScaleDown ActionSpec = "scaleDown"
)

// Status show the current status of policy
type Status struct {
	CreationTimestamp *metav1.Time
	LastScheduleTime  *metav1.Time
}

// PolicySpec define the spec of the policy
type PolicySpec struct {
	Action         ActionSpec                              `json:"action"`
	Schedule       string                                  `json:"schedule"`
	ScaleTargetRef autoscaling.CrossVersionObjectReference `json:"scaleTargetRef"`
	TargetReplicas int32                                   `json:"replicas,omitempty"`
	Status         Status                                  `json:"status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Policy represents a JSON object for the CronTab custom resource that we register in the Kubernetes API.
type Policy struct {
	// The following fields mirror the fields in the third party resource.
	metav1.TypeMeta `json:",inline"`
	ObjectMeta      metav1.ObjectMeta `json:"metadata"`
	Spec            PolicySpec        `json:"spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PolicyList is a list of Pods.
type PolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Policy `json:"items"`
}
