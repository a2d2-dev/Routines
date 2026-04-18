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

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	routinesv1alpha1 "github.com/a2d2-dev/routines/api/v1alpha1"
)

// WebhookTriggerReconciler reconciles a WebhookTrigger object.
type WebhookTriggerReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	GatewayURL string // base URL of the Gateway (used to compute public webhook URL)
}

// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=webhooktriggers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=routines.a2d2.dev,resources=webhooktriggers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile watches WebhookTrigger CRs and populates status.publicURL so external
// callers know where to POST events. The Gateway watches WebhookTrigger objects via
// its informer and registers the route dynamically.
func (r *WebhookTriggerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	trigger := &routinesv1alpha1.WebhookTrigger{}
	if err := r.Get(ctx, req.NamespacedName, trigger); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching WebhookTrigger: %w", err)
	}

	if !trigger.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Compute the public URL for this webhook.
	// The Gateway routes webhooks at /webhooks/<trigger-name>.
	gatewayBase := r.GatewayURL
	if gatewayBase == "" {
		gatewayBase = defaultGatewayURL
	}
	publicURL := fmt.Sprintf("%s/webhooks/%s", gatewayBase, trigger.Name)

	// Validate that the secret exists when required.
	ready := true
	reason := "Ready"
	message := "WebhookTrigger is ready"

	if trigger.Spec.SignatureScheme != routinesv1alpha1.SignatureSchemeNone && trigger.Spec.SecretRef != nil {
		// Verify the signing secret exists; content validation is the Gateway's job.
		secret := &corev1.Secret{}
		secretKey := types.NamespacedName{Name: trigger.Spec.SecretRef.Name, Namespace: trigger.Namespace}
		if err := r.Get(ctx, secretKey, secret); err != nil {
			if errors.IsNotFound(err) {
				ready = false
				reason = "SecretNotFound"
				message = fmt.Sprintf("signing secret %q not found", trigger.Spec.SecretRef.Name)
			}
		}
	}

	patch := client.MergeFrom(trigger.DeepCopy())
	trigger.Status.PublicURL = publicURL
	trigger.Status.Conditions = []metav1.Condition{
		{
			Type:               "Ready",
			Status:             conditionStatus(ready),
			Reason:             reason,
			Message:            message,
			LastTransitionTime: metav1.Now(),
		},
	}
	if err := r.Status().Patch(ctx, trigger, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching WebhookTrigger status: %w", err)
	}

	logf.FromContext(ctx).Info("Reconciled WebhookTrigger",
		"name", trigger.Name,
		"publicURL", publicURL,
		"ready", ready,
	)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the WebhookTrigger controller.
func (r *WebhookTriggerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&routinesv1alpha1.WebhookTrigger{}).
		Named("webhooktrigger").
		Complete(r)
}

// conditionStatus converts a bool to metav1.ConditionStatus.
func conditionStatus(ready bool) metav1.ConditionStatus {
	if ready {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}
