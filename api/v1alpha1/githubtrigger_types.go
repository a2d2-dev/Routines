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

// GitHubInstallationRef references a GitHub App installation Secret.
type GitHubInstallationRef struct {
	// Name is the name of the Secret holding the GitHub App installation credentials.
	Name string `json:"name"`
}

// GitHubRepositoryRef identifies a GitHub repository.
type GitHubRepositoryRef struct {
	// Owner is the GitHub organisation or user that owns the repository.
	Owner string `json:"owner"`
	// Name is the repository name.
	Name string `json:"name"`
}

// GitHubTriggerSpec defines the desired state of GitHubTrigger.
type GitHubTriggerSpec struct {
	// InstallationRef references the Secret containing the GitHub App installation credentials.
	InstallationRef GitHubInstallationRef `json:"installationRef"`

	// Repositories scopes the trigger to specific repositories.
	// If empty, all repositories accessible to the installation are watched.
	// +optional
	Repositories []GitHubRepositoryRef `json:"repositories,omitempty"`

	// Events is the list of GitHub event types to subscribe to (e.g. push, pull_request, issues).
	// +kubebuilder:validation:MinItems=1
	Events []string `json:"events"`

	// Filter is a CEL expression evaluated against the event payload.
	// Only events where the filter evaluates to true will fire the trigger.
	// +optional
	Filter string `json:"filter,omitempty"`

	// RoutineRefs lists the Routines that this trigger fires.
	// +kubebuilder:validation:MinItems=1
	RoutineRefs []RoutineRef `json:"routineRefs"`
}

// GitHubTriggerStatus defines the observed state of GitHubTrigger.
type GitHubTriggerStatus struct {
	// LastReceivedAt is the timestamp of the last GitHub event delivery.
	// +optional
	LastReceivedAt *metav1.Time `json:"lastReceivedAt,omitempty"`

	// Conditions contains the latest observations of the GitHubTrigger's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Installation",type="string",JSONPath=".spec.installationRef.name"
// +kubebuilder:printcolumn:name="Events",type="string",JSONPath=".spec.events"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// GitHubTrigger fires linked Routines in response to GitHub App webhook events.
type GitHubTrigger struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GitHubTriggerSpec   `json:"spec,omitempty"`
	Status GitHubTriggerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GitHubTriggerList contains a list of GitHubTrigger.
type GitHubTriggerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GitHubTrigger `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GitHubTrigger{}, &GitHubTriggerList{})
}
