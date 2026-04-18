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

// Package integration provides end-to-end integration tests for the Routines
// operator and Gateway. Tests run against a real envtest Kubernetes API server
// and an in-process Gateway instance — no external cluster is required.
package integration

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	routinesv1alpha1 "github.com/a2d2-dev/routines/api/v1alpha1"
	"github.com/a2d2-dev/routines/internal/controller"
	"github.com/a2d2-dev/routines/internal/gateway"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client

	// gwServer is the in-process gateway used for integration tests.
	gwServer *gateway.Server
	// gwDataDir is the temporary directory used as the gateway data root.
	gwDataDir string
	// gwAddr is the listen address of the in-process gateway.
	gwAddr string
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Integration Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("adding schemes")
	Expect(routinesv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(appsv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme.Scheme)).To(Succeed())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if dir := getEnvTestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	By("starting in-process gateway")
	gwDataDir, err = os.MkdirTemp("", "routines-gw-*")
	Expect(err).NotTo(HaveOccurred())

	gwAddr = "127.0.0.1:18989"
	gwServer = gateway.NewServer(gateway.Config{
		DataRoot:       gwDataDir,
		ListenAddr:     gwAddr,
		LeaseTTL:       gateway.DefaultLeaseTTL,
		ReaperInterval: gateway.DefaultReaperInterval,
		DefaultWait:    5 * time.Second,
		MaxWait:        10 * time.Second,
		WebhookSecrets: map[string]string{},
	})
	go func() {
		if err := gwServer.Start(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintf(GinkgoWriter, "gateway stopped: %v\n", err)
		}
	}()
	// Wait for the gateway to be ready by polling /healthz.
	Eventually(func() error {
		resp, err := http.Get("http://" + gwAddr + "/healthz")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("healthz returned %d", resp.StatusCode)
		}
		return nil
	}, 5*time.Second, 100*time.Millisecond).Should(Succeed(), "gateway did not become ready")

	By("starting controller manager")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // disable metrics server to avoid port conflicts in tests
		},
		HealthProbeBindAddress: "0", // disable health probe server
	})
	Expect(err).NotTo(HaveOccurred())

	Expect((&controller.RoutineReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		AgentImage: "test-agent:latest",
		GatewayURL: "http://" + gwAddr,
	}).SetupWithManager(mgr)).To(Succeed())

	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintf(GinkgoWriter, "manager stopped: %v\n", err)
		}
	}()
})

var _ = AfterSuite(func() {
	By("tearing down")
	cancel()
	Eventually(func() error {
		return testEnv.Stop()
	}, time.Minute, time.Second).Should(Succeed())
	_ = os.RemoveAll(gwDataDir)
})

func getEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}
