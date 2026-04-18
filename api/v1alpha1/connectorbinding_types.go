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

// ConnectorScope defines the access level granted by a ConnectorBinding.
// +kubebuilder:validation:Enum=readonly;readwrite
type ConnectorScope string

const (
	ConnectorScopeReadOnly  ConnectorScope = "readonly"
	ConnectorScopeReadWrite ConnectorScope = "readwrite"
)

// InjectAs defines how a credential is surfaced inside the agent pod.
// +kubebuilder:validation:Enum=env;file
type InjectAs string

const (
	InjectAsEnv  InjectAs = "env"
	InjectAsFile InjectAs = "file"
)

// InjectRule defines how a single key from the referenced Secret is injected.
type InjectRule struct {
	// As specifies the injection mode (env or file).
	// +kubebuilder:default=env
	As InjectAs `json:"as"`

	// Key is the key within the referenced Secret.
	Key string `json:"key"`

	// EnvName is the environment variable name used when As=env.
	// +optional
	EnvName string `json:"envName,omitempty"`

	// MountPath is the file path used when As=file.
	// +optional
	MountPath string `json:"mountPath,omitempty"`
}

// ConnectorBindingSpec defines the desired state of ConnectorBinding.
type ConnectorBindingSpec struct {
	// SecretRef references the Kubernetes Secret that holds the connector credentials.
	SecretRef SecretRef `json:"secretRef"`

	// Scope defines the access level granted by this binding.
	// +kubebuilder:default=readonly
	Scope ConnectorScope `json:"scope,omitempty"`

	// Inject defines how Secret keys are surfaced inside the agent pod.
	// +kubebuilder:validation:MinItems=1
	Inject []InjectRule `json:"inject"`
}

// ConnectorBindingStatus defines the observed state of ConnectorBinding.
type ConnectorBindingStatus struct {
	// Conditions contains the latest observations of the ConnectorBinding's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Secret",type="string",JSONPath=".spec.secretRef.name"
// +kubebuilder:printcolumn:name="Scope",type="string",JSONPath=".spec.scope"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ConnectorBinding binds a Kubernetes Secret to a named connector so the Controller
// can inject credentials into agent pods without encoding provider-specific env var names
// into the Routine spec.
type ConnectorBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConnectorBindingSpec   `json:"spec,omitempty"`
	Status ConnectorBindingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConnectorBindingList contains a list of ConnectorBinding.
type ConnectorBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConnectorBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConnectorBinding{}, &ConnectorBindingList{})
}
