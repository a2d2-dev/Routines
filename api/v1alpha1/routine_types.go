/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PromptSpec defines the prompt content for a Routine.
type PromptSpec struct {
	// Inline is an inline prompt string.
	// +optional
	Inline string `json:"inline,omitempty"`

	// ConfigMapRef references a ConfigMap key containing the prompt.
	// +optional
	ConfigMapRef *ConfigMapKeyRef `json:"configMapRef,omitempty"`
}

// ConfigMapKeyRef references a key in a ConfigMap.
type ConfigMapKeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// RepositoryRef references a git repository.
type RepositoryRef struct {
	// Name is the name of the repository resource or URL.
	Name string `json:"name"`
}

// TriggerRef references a trigger resource.
type TriggerRef struct {
	// Kind is the trigger kind (ScheduleTrigger, WebhookTrigger, GitHubTrigger).
	Kind string `json:"kind"`
	// Name is the trigger resource name.
	Name string `json:"name"`
}

// ConnectorBindingRef references a ConnectorBinding by name.
type ConnectorBindingRef struct {
	// Name is the ConnectorBinding resource name.
	Name string `json:"name"`
}

// RoutineSpec defines the desired state of Routine.
type RoutineSpec struct {
	// Prompt defines the AI prompt for this Routine.
	Prompt PromptSpec `json:"prompt"`

	// RepositoryRef optionally references a git repository to check out before each run.
	// +optional
	RepositoryRef *RepositoryRef `json:"repositoryRef,omitempty"`

	// ConnectorBindingRefs lists ConnectorBindings whose credentials are injected into the agent pod.
	// +optional
	ConnectorBindingRefs []ConnectorBindingRef `json:"connectorBindingRefs,omitempty"`

	// TriggerRefs lists triggers that fire this Routine.
	// +optional
	TriggerRefs []TriggerRef `json:"triggerRefs,omitempty"`

	// MaxDurationSeconds limits how long a single agent run may last.
	// Defaults to 1800 (30 minutes).
	// +optional
	// +kubebuilder:default=1800
	MaxDurationSeconds int32 `json:"maxDurationSeconds,omitempty"`

	// Suspend, when true, scales the agent pod to zero without deleting its PVC.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// RoutinePhase describes the lifecycle phase of a Routine.
// +kubebuilder:validation:Enum=Pending;Ready;Suspended;Terminating
type RoutinePhase string

const (
	RoutinePhasePending     RoutinePhase = "Pending"
	RoutinePhaseReady       RoutinePhase = "Ready"
	RoutinePhaseSuspended   RoutinePhase = "Suspended"
	RoutinePhaseTerminating RoutinePhase = "Terminating"
)

// RoutineStatus defines the observed state of Routine.
type RoutineStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase RoutinePhase `json:"phase,omitempty"`

	// PodReady indicates whether the agent pod is ready.
	// +optional
	PodReady bool `json:"podReady,omitempty"`

	// CurrentMessageID is the ID of the message currently being processed.
	// +optional
	CurrentMessageID string `json:"currentMessageID,omitempty"`

	// LastMessageAt is the timestamp of the last processed message.
	// +optional
	LastMessageAt *metav1.Time `json:"lastMessageAt,omitempty"`

	// GatewayRegistered indicates that the Gateway has registered the queue directory for this Routine.
	// +optional
	GatewayRegistered bool `json:"gatewayRegistered,omitempty"`

	// Conditions contains the latest observations of the Routine's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="PodReady",type="boolean",JSONPath=".status.podReady"
// +kubebuilder:printcolumn:name="Suspend",type="boolean",JSONPath=".spec.suspend"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Routine is the Schema for the routines API.
// It declares an AI agent that is triggered by schedules, webhooks, or GitHub events.
type Routine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RoutineSpec   `json:"spec,omitempty"`
	Status RoutineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RoutineList contains a list of Routine.
type RoutineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Routine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Routine{}, &RoutineList{})
}
