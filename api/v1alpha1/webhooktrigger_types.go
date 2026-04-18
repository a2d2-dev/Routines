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

// SignatureScheme defines how incoming webhook requests are authenticated.
// +kubebuilder:validation:Enum=hmac;bearer;none
type SignatureScheme string

const (
	SignatureSchemeHMAC   SignatureScheme = "hmac"
	SignatureSchemeBearer SignatureScheme = "bearer"
	SignatureSchemeNone   SignatureScheme = "none"
)

// SecretRef references a Kubernetes Secret.
type SecretRef struct {
	// Name is the Secret resource name.
	Name string `json:"name"`
}

// WebhookTriggerSpec defines the desired state of WebhookTrigger.
type WebhookTriggerSpec struct {
	// SignatureScheme specifies how the webhook payload is authenticated.
	// +kubebuilder:default=hmac
	SignatureScheme SignatureScheme `json:"signatureScheme,omitempty"`

	// SecretRef references a Secret containing the signing key.
	// Required when signatureScheme is hmac or bearer.
	// +optional
	SecretRef *SecretRef `json:"secretRef,omitempty"`

	// RoutineRefs lists the Routines that this trigger fires.
	// +kubebuilder:validation:MinItems=1
	RoutineRefs []RoutineRef `json:"routineRefs"`
}

// WebhookTriggerStatus defines the observed state of WebhookTrigger.
type WebhookTriggerStatus struct {
	// PublicURL is the external URL at which this webhook can be reached.
	// +optional
	PublicURL string `json:"publicURL,omitempty"`

	// LastReceivedAt is the timestamp of the last webhook delivery.
	// +optional
	LastReceivedAt *metav1.Time `json:"lastReceivedAt,omitempty"`

	// Conditions contains the latest observations of the WebhookTrigger's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Scheme",type="string",JSONPath=".spec.signatureScheme"
// +kubebuilder:printcolumn:name="PublicURL",type="string",JSONPath=".status.publicURL"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// WebhookTrigger fires linked Routines when an authenticated HTTP POST is received.
type WebhookTrigger struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WebhookTriggerSpec   `json:"spec,omitempty"`
	Status WebhookTriggerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WebhookTriggerList contains a list of WebhookTrigger.
type WebhookTriggerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WebhookTrigger `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WebhookTrigger{}, &WebhookTriggerList{})
}
