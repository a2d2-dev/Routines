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

// RoutineRef references a Routine by name and optional namespace.
type RoutineRef struct {
	// Name is the Routine resource name.
	Name string `json:"name"`
	// Namespace is the namespace of the Routine.
	// Defaults to the trigger's namespace if omitted.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// ScheduleTriggerSpec defines the desired state of ScheduleTrigger.
type ScheduleTriggerSpec struct {
	// Cron is a standard cron expression (5-field: minute hour dom month dow).
	// +kubebuilder:validation:Pattern=`^(\*|([0-9]|1[0-9]|2[0-9]|3[0-9]|4[0-9]|5[0-9])|\*\/([0-9]|1[0-9]|2[0-9]|3[0-9]|4[0-9]|5[0-9])) (\*|([0-9]|1[0-9]|2[0-3])|\*\/([0-9]|1[0-9]|2[0-3])) (\*|([1-9]|1[0-9]|2[0-9]|3[0-1])|\*\/([1-9]|1[0-9]|2[0-9]|3[0-1])) (\*|([1-9]|1[0-2])|\*\/([1-9]|1[0-2])) (\*|([0-6])|\*\/([0-6]))$`
	Cron string `json:"cron"`

	// Timezone is the IANA timezone name for the cron schedule.
	// Defaults to UTC.
	// +optional
	// +kubebuilder:default="UTC"
	Timezone string `json:"timezone,omitempty"`

	// RoutineRefs lists the Routines that this trigger fires.
	// +kubebuilder:validation:MinItems=1
	RoutineRefs []RoutineRef `json:"routineRefs"`
}

// ScheduleTriggerStatus defines the observed state of ScheduleTrigger.
type ScheduleTriggerStatus struct {
	// LastFiredAt is the timestamp of the last time this trigger fired.
	// +optional
	LastFiredAt *metav1.Time `json:"lastFiredAt,omitempty"`

	// Conditions contains the latest observations of the ScheduleTrigger's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Cron",type="string",JSONPath=".spec.cron"
// +kubebuilder:printcolumn:name="Timezone",type="string",JSONPath=".spec.timezone"
// +kubebuilder:printcolumn:name="LastFired",type="date",JSONPath=".status.lastFiredAt"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ScheduleTrigger fires linked Routines on a cron schedule.
type ScheduleTrigger struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ScheduleTriggerSpec   `json:"spec,omitempty"`
	Status ScheduleTriggerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ScheduleTriggerList contains a list of ScheduleTrigger.
type ScheduleTriggerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScheduleTrigger `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ScheduleTrigger{}, &ScheduleTriggerList{})
}
