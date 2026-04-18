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

package integration

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	routinesv1alpha1 "github.com/a2d2-dev/routines/api/v1alpha1"
)

var _ = Describe("ConnectorBinding env rendering", func() {
	Context("when a Routine references a ConnectorBinding", func() {
		var (
			routineName   string
			connectorName string
			secretName    string
		)

		BeforeEach(func() {
			suffix := generateSuffix()
			routineName = "cb-routine-" + suffix
			connectorName = "cb-connector-" + suffix
			secretName = "cb-secret-" + suffix

			By("creating the backing Secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: testNamespace,
				},
				StringData: map[string]string{
					"ANTHROPIC_API_KEY": "test-api-key-value",
					"GITHUB_TOKEN":      "test-github-token",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("creating the ConnectorBinding")
			cb := &routinesv1alpha1.ConnectorBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      connectorName,
					Namespace: testNamespace,
				},
				Spec: routinesv1alpha1.ConnectorBindingSpec{
					SecretRef: routinesv1alpha1.SecretRef{
						Name: secretName,
					},
					Inject: []routinesv1alpha1.InjectRule{
						{As: routinesv1alpha1.InjectAsEnv, Key: "ANTHROPIC_API_KEY", EnvName: "ANTHROPIC_API_KEY"},
						{As: routinesv1alpha1.InjectAsEnv, Key: "GITHUB_TOKEN", EnvName: "GITHUB_TOKEN"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cb)).To(Succeed())
		})

		AfterEach(func() {
			// Cleanup
			for _, name := range []string{routineName} {
				r := &routinesv1alpha1.Routine{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNamespace, Name: name,
				}, r); err == nil {
					_ = k8sClient.Delete(ctx, r)
				}
			}
		})

		It("injects ConnectorBinding env vars into the agent StatefulSet", func() {
			routine := &routinesv1alpha1.Routine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      routineName,
					Namespace: testNamespace,
				},
				Spec: routinesv1alpha1.RoutineSpec{
					Prompt: routinesv1alpha1.PromptSpec{
						Inline: "Process GitHub events with Claude.",
					},
					ConnectorBindingRefs: []routinesv1alpha1.ConnectorBindingRef{
						{Name: connectorName},
					},
				},
			}
			Expect(k8sClient.Create(ctx, routine)).To(Succeed())

			By("waiting for StatefulSet to be created with env vars")
			Eventually(func(g Gomega) {
				sts := &appsv1.StatefulSet{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNamespace, Name: routineName,
				}, sts)).To(Succeed())

				g.Expect(sts.Spec.Template.Spec.Containers).NotTo(BeEmpty())
				container := sts.Spec.Template.Spec.Containers[0]

				// The controller should inject env vars from the ConnectorBinding.
				envNames := make([]string, len(container.Env))
				for i, e := range container.Env {
					envNames[i] = e.Name
				}
				// At minimum, AGENT_GATEWAY_URL and AGENT_ROUTINE_UID should be present.
				g.Expect(envNames).To(ContainElement("AGENT_GATEWAY_URL"))
				g.Expect(envNames).To(ContainElement("AGENT_ROUTINE_UID"))
			}, timeout, interval).Should(Succeed())
		})
	})
})
