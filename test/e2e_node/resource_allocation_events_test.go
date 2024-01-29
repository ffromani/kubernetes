/*
Copyright 2023 The Kubernetes Authors.

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

package e2enode

import (
	"context"
	"fmt"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	kubeletconfig "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/test/e2e/feature"
	"k8s.io/kubernetes/test/e2e/framework"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"
	e2eskipper "k8s.io/kubernetes/test/e2e/framework/skipper"
	admissionapi "k8s.io/pod-security-admission/api"
)

var _ = SIGDescribe("Resource Allocation Events", framework.WithSerial(), feature.TopologyManager, feature.CPUManager, feature.MemoryManager, feature.DeviceManager, func() {
	f := framework.NewDefaultFramework("resource-allocation-events")
	f.NamespacePodSecurityLevel = admissionapi.LevelPrivileged

	ginkgo.Context("with topology and cpumanager configured", func() {
		var oldCfg *kubeletconfig.KubeletConfiguration
		var testPod *corev1.Pod
		var cpusNumPerNUMA, coresNumPerNUMA, numaNodes, threadsPerCore int

		ginkgo.BeforeEach(func(ctx context.Context) {
			var err error
			if oldCfg == nil {
				oldCfg, err = getCurrentKubeletConfig(ctx)
				framework.ExpectNoError(err)
			}

			numaNodes = detectNUMANodes()
			coresNumPerNUMA = detectCoresPerSocket()
			if coresNumPerNUMA < minCoreCount {
				e2eskipper.Skipf("this test is intended to be run on a system with at least %d cores per socket", minCoreCount)
			}
			threadsPerCore = detectThreadPerCore()
			cpusNumPerNUMA = coresNumPerNUMA * threadsPerCore

			// It is safe to assume that the CPUs are distributed equally across
			// NUMA nodes and therefore number of CPUs on all NUMA nodes are same
			// so we just check the CPUs on the first NUMA node

			framework.Logf("numaNodes on the system %d", numaNodes)
			framework.Logf("Cores per NUMA on the system %d", coresNumPerNUMA)
			framework.Logf("Threads per Core on the system %d", threadsPerCore)
			framework.Logf("CPUs per NUMA on the system %d", cpusNumPerNUMA)

			policy := topologymanager.PolicyRestricted // events will be emitted anyway
			scope := podScopeTopology                  // not relevant

			newCfg, _ := configureTopologyManagerInKubelet(oldCfg, policy, scope, nil, 0)
			updateKubeletConfig(ctx, f, newCfg, true)
		})

		ginkgo.AfterEach(func(ctx context.Context) {
			if testPod != nil {
				deletePodSyncByName(ctx, f, testPod.Name)
			}
			updateKubeletConfig(ctx, f, oldCfg, true)
		})

		ginkgo.It("should emit event when resource allocation is successful", func(ctx context.Context) {
			ginkgo.By("Creating the test pod which will be admitted")
			testPod = e2epod.NewPodClient(f).Create(ctx, makeGuaranteedCPUExclusiveSleeperPod("pinned-allocation", threadsPerCore))

			events, err := getEventsForPod(f.ClientSet, testPod.Namespace, testpod.Name)
			framework.ExpectNoError(err)

			foundEvent := false
			for _, event := range events {
				framework.Logf("found event: [%+v]", event)
				if event.Reason == "AllocatedAlignedResources" && len(event.Message) > 0 {
					foundEvent = true
					break
				}
			}
			gomega.Expect(foundEvent).To(gomega.BeTrue(), "missing event reporting resource allocation")
		})
	})
})

func getEventsForPod(ctx context.Context, cli kubernetes.Interface, podNamespace, podName string) ([]corev1.Event, error) {
	opts := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", podName),
		TypeMeta:      metav1.TypeMeta{Kind: "Pod"},
	}
	events, err := cli.CoreV1().Events(podNamespace).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	return events.Items, nil
}
