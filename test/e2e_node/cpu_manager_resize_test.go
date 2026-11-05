/*
Copyright 2026 The Kubernetes Authors.

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
	"regexp"
	"strconv"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/features"
	kubeletconfig "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager"
	"k8s.io/kubernetes/test/e2e/common/node/framework/cgroups"
	"k8s.io/kubernetes/test/e2e/common/node/framework/podresize"
	admissionapi "k8s.io/pod-security-admission/api"
	"k8s.io/utils/cpuset"

	"k8s.io/kubernetes/test/e2e/feature"
	"k8s.io/kubernetes/test/e2e/framework"
	e2enode "k8s.io/kubernetes/test/e2e/framework/node"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"
	e2eskipper "k8s.io/kubernetes/test/e2e/framework/skipper"
)

type containerCPUInfo struct {
	Name     string
	InitCtr  bool
	cpuCount int
}

type podResizeEnv struct {
	fwk           *framework.Framework
	createPodSync func(context.Context, *v1.Pod) *v1.Pod
}

type resizePhase string

const (
	resizePhasePatch     resizePhase = "patch"
	resizePhaseActuation resizePhase = "actuation"
)

type resizeError struct {
	errorString string
	phase       resizePhase
}

type podResizeStep struct {
	desiredContainers  []podresize.ResizableContainerInfo
	expectedContainers []podresize.ResizableContainerInfo
	wantError          resizeError
	// terminal stops the sequence after verifying this step's expected outcome,
	// whether success or failure. Otherwise, subsequent steps run, allowing recovery tests.
	terminal bool
}

func exclusiveCPUCount(container podresize.ResizableContainerInfo) int {
	ginkgo.GinkgoHelper()

	gomega.Expect(container.Resources).NotTo(gomega.BeNil(), "container %q must specify resources", container.Name)
	cpu := resource.MustParse(container.Resources.CPUReq)
	gomega.Expect(cpu.MilliValue()).To(gomega.BeNumerically(">", 0), "container %q must request exclusive CPUs", container.Name)
	gomega.Expect(cpu.MilliValue()%1000).To(gomega.BeZero(), "container %q must request an integer number of CPUs", container.Name)
	return int(cpu.Value())
}

func checkAllocatedCPUsForResizeStep(ctx context.Context, f *framework.Framework, containers []podresize.ResizableContainerInfo, pod *v1.Pod) {
	ginkgo.GinkgoHelper()

	cpuInfo := make([]containerCPUInfo, len(containers))
	for i, container := range containers {
		cpuInfo[i] = containerCPUInfo{
			Name:     container.Name,
			InitCtr:  container.InitCtr,
			cpuCount: exclusiveCPUCount(container),
		}
	}
	checkAllocatedCPUsForResize(ctx, f, cpuInfo, pod)
}

func containersForCPUs(cpus int) []podresize.ResizableContainerInfo {
	cpuQuantity := strconv.Itoa(cpus)
	return []podresize.ResizableContainerInfo{
		{
			Name:      "gu-container-1",
			Resources: &cgroups.ContainerResources{CPUReq: cpuQuantity, CPULim: cpuQuantity, MemReq: "300Mi", MemLim: "300Mi"},
		},
	}
}

func requiredCPUsForResizeSequence(originalContainers []podresize.ResizableContainerInfo, steps ...podResizeStep) int {
	ginkgo.GinkgoHelper()

	requiredCPUs := 0
	for _, container := range originalContainers {
		requiredCPUs = max(requiredCPUs, exclusiveCPUCount(container))
	}
	for _, step := range steps {
		for _, container := range step.expectedContainers {
			requiredCPUs = max(requiredCPUs, exclusiveCPUCount(container))
		}
	}
	return requiredCPUs
}

func applyPodResizeSequence(ctx context.Context, env podResizeEnv, originalContainers []podresize.ResizableContainerInfo, steps ...podResizeStep) {
	ginkgo.GinkgoHelper()

	cpuCapacity, cpuAllocatable, cpuReserved := getLocalNodeCPUDetails(ctx, env.fwk)
	framework.Logf("Node CPU resources: capacity=%d allocatable=%d reserved=%d", cpuCapacity, cpuAllocatable, cpuReserved)

	podClient := e2epod.NewPodClient(env.fwk)
	testPod := podresize.MakePodWithResizableContainers(env.fwk.Namespace.Name, "testpod", strconv.Itoa(time.Now().Nanosecond()), originalContainers, nil)
	testPod = e2epod.MustMixinRestrictedPodSecurity(testPod)
	ginkgo.By("creating the test pod")
	testPod = env.createPodSync(ctx, testPod)
	podresize.VerifyPodResources(testPod, originalContainers, nil)
	checkAllocatedCPUsForResizeStep(ctx, env.fwk, originalContainers, testPod)

	currentContainers := originalContainers
	for i, step := range steps {
		ginkgo.By(fmt.Sprintf("applying resize step %d of %d", i+1, len(steps)))
		beforePatch, err := podClient.Get(ctx, testPod.Name, metav1.GetOptions{})
		framework.ExpectNoError(err, "failed to get pod before resize step %d", i+1)
		expected := podresize.UpdateExpectedContainerRestarts(ctx, beforePatch, step.expectedContainers)
		patch := podresize.MakeResizePatch(currentContainers, step.desiredContainers, nil, nil)

		patchedPod, err := podClient.Patch(ctx, testPod.Name, apimachinerytypes.StrategicMergePatchType, patch, metav1.PatchOptions{}, "resize")
		if step.wantError.phase == resizePhasePatch {
			gomega.Expect(err).To(gomega.HaveOccurred(), "resize step %d must be rejected", i+1)
			gomega.Expect(err).To(gomega.MatchError(gomega.MatchRegexp(step.wantError.errorString)))

			testPod, err = podClient.Get(ctx, testPod.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "failed to get pod after rejected resize step %d", i+1)
			podresize.VerifyPodResources(testPod, step.expectedContainers, nil)
			checkAllocatedCPUsForResizeStep(ctx, env.fwk, step.expectedContainers, testPod)
			if step.terminal {
				return
			}
			continue
		}
		framework.ExpectNoError(err, "failed to patch pod for resize step %d", i+1)
		currentContainers = step.desiredContainers

		if step.wantError.phase == resizePhaseActuation {
			ginkgo.By("waiting for the resize to become infeasible")
			WaitForPodResizeInfeasible(ctx, env.fwk, patchedPod)

			testPod, err = podClient.Get(ctx, testPod.Name, metav1.GetOptions{})
			framework.ExpectNoError(err, "failed to get pod after infeasible resize step %d", i+1)
			gomega.Expect(testPod).To(HaveStatusConditionsMatchingRegex(step.wantError.errorString))
			checkAllocatedCPUsForResizeStep(ctx, env.fwk, step.expectedContainers, testPod)
			if step.terminal {
				return
			}
			continue
		}

		ginkgo.By("waiting for the resize to be actuated")
		testPod = podresize.WaitForPodResizeActuation(ctx, env.fwk, podClient, patchedPod, expected)
		podresize.VerifyPodResources(testPod, step.expectedContainers, nil)

		ginkgo.By("verifying the container cpusets after the resize step")
		checkAllocatedCPUsForResizeStep(ctx, env.fwk, step.expectedContainers, testPod)
		if step.terminal {
			return
		}
	}
}

var _ = SIGDescribe("CPU Manager with InPlacePodVerticalScalingExclusiveCPUs enabled", ginkgo.Ordered, ginkgo.ContinueOnFailure, framework.WithSerial(), feature.CPUManager, feature.InPlacePodVerticalScaling, feature.InPlacePodVerticalScalingExclusiveCPUs, framework.WithFeatureGate(features.InPlacePodVerticalScaling), framework.WithFeatureGate(features.InPlacePodVerticalScalingExclusiveCPUs), func() {
	f := framework.NewDefaultFramework("cpu-manager-pod-resize-test")
	f.NamespacePodSecurityLevel = admissionapi.LevelPrivileged

	// original kubeletconfig before the context start, to be restored
	var oldCfg *kubeletconfig.KubeletConfiguration
	var reservedCPUs cpuset.CPUSet
	var onlineCPUs cpuset.CPUSet
	var smtLevel int
	var uncoreGroupSize int
	// tracks all the pods created by a It() block. Best would be a namespace per It block
	var podMap map[string]*v1.Pod
	var skipIfAllocatableCPUsLessThan func(node *v1.Node, cpuReq int)

	var createPodSync func(ctx context.Context, pod *v1.Pod) *v1.Pod

	var testPodResize func(ctx context.Context, originalContainers []podresize.ResizableContainerInfo, originalCpuInfo []containerCPUInfo, desiredContainersFirstPatch []podresize.ResizableContainerInfo, expectedContainersFirstPatch []podresize.ResizableContainerInfo, expectedCpuInfoFirstPatch []containerCPUInfo, wantPatchErrorFirstPatch string, wantActuationErrorFirstPatch string, desiredContainersSecondPatch []podresize.ResizableContainerInfo, expectedContainersSecondPatch []podresize.ResizableContainerInfo, expectedCpuInfoSecondPatch []containerCPUInfo, wantPatchErrorSecondPatch string, wantActuationErrorSecondPatch string, podMap map[string]*v1.Pod)

	ginkgo.BeforeAll(func(ctx context.Context) {
		var err error
		oldCfg, err = getCurrentKubeletConfig(ctx)
		framework.ExpectNoError(err)

		onlineCPUs, err = getOnlineCPUs() // this should not change at all, at least during this suite lifetime
		framework.ExpectNoError(err)
		framework.Logf("Online CPUs: %s", onlineCPUs)

		uncoreGroupSize = getUncoreCPUGroupSize()
		framework.Logf("Uncore Group Size: %d", uncoreGroupSize)

		e2enodeCgroupV2Enabled = IsCgroup2UnifiedMode()
		framework.Logf("cgroup V2 enabled: %v", e2enodeCgroupV2Enabled)

		e2enodeCgroupDriver = oldCfg.CgroupDriver
		framework.Logf("cgroup driver: %s", e2enodeCgroupDriver)

		runtime, _, err := getCRIClient(ctx)
		framework.ExpectNoError(err, "Failed to get CRI client")

		version, err := runtime.Version(ctx, "")
		framework.ExpectNoError(err, "Failed to get runtime version")

		e2enodeRuntimeName = version.GetRuntimeName()
		framework.Logf("runtime: %s", e2enodeRuntimeName)
	})

	ginkgo.AfterAll(func(ctx context.Context) {
		updateKubeletConfig(ctx, f, oldCfg, true)
	})

	ginkgo.BeforeEach(func(ctx context.Context) {
		// note intentionally NOT set reservedCPUs -  this must be initialized on a test-by-test basis
		podMap = make(map[string]*v1.Pod)
		createPodSync = func(ctx context.Context, pod *v1.Pod) *v1.Pod {
			newPod := e2epod.NewPodClient(f).CreateSync(ctx, pod)
			podMap[string(newPod.UID)] = newPod
			return newPod
		}

		testPodResize = func(ctx context.Context, originalContainers []podresize.ResizableContainerInfo, originalCpuInfo []containerCPUInfo, desiredContainersFirstPatch []podresize.ResizableContainerInfo, expectedContainersFirstPatch []podresize.ResizableContainerInfo, expectedCpuInfoFirstPatch []containerCPUInfo, wantPatchErrorFirstPatch string, wantActuationErrorFirstPatch string, desiredContainersSecondPatch []podresize.ResizableContainerInfo, expectedContainersSecondPatch []podresize.ResizableContainerInfo, expectedCpuInfoSecondPatch []containerCPUInfo, wantPatchErrorSecondPatch string, wantActuationErrorSecondPatch string, podMap map[string]*v1.Pod) {

			podClient := e2epod.NewPodClient(f)
			nodes, err := e2enode.GetReadySchedulableNodes(ctx, f.ClientSet)
			framework.ExpectNoError(err, "failed to get running nodes")
			gomega.Expect(nodes.Items).ShouldNot(gomega.BeEmpty())
			framework.Logf("Found %d schedulable nodes", len(nodes.Items))

			ginkgo.By("Find node CPU resources available for allocation!")
			node := nodes.Items[0]
			nodeAllocatableCPU, nodeAvailableCPU, err := e2enode.GetNodeAllocatableAndAvailableQuantities(ctx, f.ClientSet, &node, v1.ResourceCPU)
			framework.ExpectNoError(err, "failed to get CPU resources available for allocation")
			framework.Logf("Node '%s': NodeAllocatable MilliCPUs = %dm. MilliCPUs currently available to allocate = %dm.",
				node.Name, nodeAllocatableCPU.MilliValue(), nodeAvailableCPU.MilliValue())

			tStamp := strconv.Itoa(time.Now().Nanosecond())
			testPod1 := podresize.MakePodWithResizableContainers(f.Namespace.Name, "testpod1", tStamp, originalContainers, nil)
			testPod1 = e2epod.MustMixinRestrictedPodSecurity(testPod1)
			e2epod.SetNodeAffinity(&testPod1.Spec, node.Name)
			ginkgo.By("creating test pod 1 with multiple containers")
			testPod1 = createPodSync(ctx, testPod1)

			ginkgo.By("verifying original pod resources, allocations and policy are as expected")
			podresize.VerifyPodResources(testPod1, originalContainers, nil)

			ginkgo.By("verifying original pod cpusets are as expected")
			checkAllocatedCPUsForResize(ctx, f, originalCpuInfo, testPod1)

			nodeAllocatableCPUAfterPodCreate, nodeAvailableCPUAfterPodCreate, err := e2enode.GetNodeAllocatableAndAvailableQuantities(ctx, f.ClientSet, &node, v1.ResourceCPU)
			framework.ExpectNoError(err, "failed to get CPU resources available for allocation")
			framework.Logf("Node '%s': NodeAllocatable MilliCPUs = %dm. MilliCPUs currently available to allocate = %dm.",
				node.Name, nodeAllocatableCPUAfterPodCreate.MilliValue(), nodeAvailableCPUAfterPodCreate.MilliValue())

			ginkgo.By("patching pod for resize")
			patchString := podresize.MakeResizePatch(originalContainers, desiredContainersFirstPatch, nil, nil)

			patchedPod, pErr := f.ClientSet.CoreV1().Pods(testPod1.Namespace).Patch(ctx,
				testPod1.Name, apimachinerytypes.StrategicMergePatchType, []byte(patchString), metav1.PatchOptions{}, "resize")

			if wantPatchErrorFirstPatch == "" {
				framework.ExpectNoError(pErr, "failed to patch pod for resize")

				if wantActuationErrorFirstPatch == "" {
					expected := podresize.UpdateExpectedContainerRestarts(ctx, patchedPod, expectedContainersFirstPatch)
					ginkgo.By("verifying pod resources are as expected post patch, pre-actuation")
					podresize.VerifyPodResources(patchedPod, expected, nil)

					ginkgo.By("waiting for resize to be actuated")
					resizedPod := podresize.WaitForPodResizeActuation(ctx, f, podClient, testPod1, nil)
					podresize.CheckPodResized(ctx, f, resizedPod, expected)

					ginkgo.By("verifying pod resources after resize")
					podresize.VerifyPodResources(resizedPod, expected, nil)

					ginkgo.By("verifying pod cpusets after resize")
					checkAllocatedCPUsForResize(ctx, f, expectedCpuInfoFirstPatch, testPod1)

					nodeAllocatableCPUAfterPodResize, nodeAvailableCPUAfterPodResize, err := e2enode.GetNodeAllocatableAndAvailableQuantities(ctx, f.ClientSet, &node, v1.ResourceCPU)
					framework.ExpectNoError(err, "failed to get CPU resources available for allocation")
					framework.Logf("Node '%s': NodeAllocatable MilliCPUs = %dm. MilliCPUs currently available to allocate = %dm.",
						node.Name, nodeAllocatableCPUAfterPodResize.MilliValue(), nodeAvailableCPUAfterPodResize.MilliValue())

					if desiredContainersSecondPatch != nil {
						ginkgo.By("patching again pod for resize")
						secondPatchString := podresize.MakeResizePatch(expected, desiredContainersSecondPatch, nil, nil)

						patchedPod, pErr := f.ClientSet.CoreV1().Pods(testPod1.Namespace).Patch(ctx,
							testPod1.Name, apimachinerytypes.StrategicMergePatchType, []byte(secondPatchString), metav1.PatchOptions{}, "resize")

						if wantPatchErrorSecondPatch == "" {
							framework.ExpectNoError(pErr, "failed to patch again pod for resize")

							if wantActuationErrorSecondPatch == "" {

								expected = podresize.UpdateExpectedContainerRestarts(ctx, patchedPod, expectedContainersSecondPatch)
								ginkgo.By("verifying pod resources are as expected post second patch, pre-actuation")
								podresize.VerifyPodResources(patchedPod, expected, nil)

								ginkgo.By("waiting for second patch resize to be actuated")
								resizedPod = podresize.WaitForPodResizeActuation(ctx, f, podClient, testPod1, nil)
								podresize.CheckPodResized(ctx, f, resizedPod, expected)

								ginkgo.By("verifying pod resources after second resize")
								podresize.VerifyPodResources(resizedPod, expected, nil)

								ginkgo.By("verifying pod cpusets after second actuated resize")
								checkAllocatedCPUsForResize(ctx, f, expectedCpuInfoSecondPatch, testPod1)

							} else {
								patchedPod, pErr = f.ClientSet.CoreV1().Pods(testPod1.Namespace).Patch(ctx,
									testPod1.Name, apimachinerytypes.StrategicMergePatchType, []byte(secondPatchString), metav1.PatchOptions{}, "resize")
								framework.ExpectNoError(pErr, "failed to patch again pod for resize")

								ginkgo.By("waiting for testing pod resize to be infeasible")
								WaitForPodResizeInfeasible(ctx, f, patchedPod)

								infeasibleResizePod, err := framework.GetObject(podClient.Get, patchedPod.Name, metav1.GetOptions{})(ctx)
								framework.ExpectNoError(err, "failed to get inreasible to resize pod")

								ginkgo.By("ensuring the testing pod is failed for the expected reason for second patch")
								gomega.Expect(infeasibleResizePod).To(HaveStatusConditionsMatchingRegex(wantActuationErrorSecondPatch))

								ginkgo.By("verifying pod cpusets after second non actuated resize")
								checkAllocatedCPUsForResize(ctx, f, expectedCpuInfoSecondPatch, testPod1)
							}

						} else {
							ginkgo.By("ensuring the testing pod is failed for the expected reason for second patch")
							re := regexp.MustCompile(wantPatchErrorSecondPatch)
							gomega.Expect(re.MatchString(pErr.Error())).To(gomega.BeTrueBecause("expected second patch error [%s] to match [%s]", pErr.Error(), wantPatchErrorSecondPatch))

							ginkgo.By("verifying pod cpusets after second non patched resize")
							checkAllocatedCPUsForResize(ctx, f, expectedCpuInfoSecondPatch, testPod1)
						}
					}
				} else {
					patchedPod, pErr := f.ClientSet.CoreV1().Pods(testPod1.Namespace).Patch(ctx,
						testPod1.Name, apimachinerytypes.StrategicMergePatchType, []byte(patchString), metav1.PatchOptions{}, "resize")
					framework.ExpectNoError(pErr, "failed to patch pod for resize")

					ginkgo.By("waiting for testing pod resize status to be infeasible")
					WaitForPodResizeInfeasible(ctx, f, patchedPod)

					infeasibleResizePod, err := framework.GetObject(podClient.Get, patchedPod.Name, metav1.GetOptions{})(ctx)
					framework.ExpectNoError(err, "failed to get infeasible to resize pod")

					ginkgo.By("ensuring the testing pod is failed for the expected reason")
					gomega.Expect(infeasibleResizePod).To(HaveStatusConditionsMatchingRegex(wantActuationErrorFirstPatch))

					ginkgo.By("verifying pod cpusets after second non patched resize")
					checkAllocatedCPUsForResize(ctx, f, expectedCpuInfoFirstPatch, testPod1)
				}
			} else {
				ginkgo.By("ensuring the testing pod is failed for the expected reason for first patch")
				re := regexp.MustCompile(wantPatchErrorFirstPatch)
				gomega.Expect(re.MatchString(pErr.Error())).To(gomega.BeTrueBecause("expected first patch error [%s] to match [%s]", pErr.Error(), wantPatchErrorFirstPatch))

				ginkgo.By("verifying pod cpusets after second non patched resize")
				checkAllocatedCPUsForResize(ctx, f, expectedCpuInfoFirstPatch, testPod1)
			}
		}

		reservedCPUs = cpuset.New()
	})

	ginkgo.JustBeforeEach(func(ctx context.Context) {
		if !e2enodeCgroupV2Enabled {
			e2eskipper.Skipf("Skipping since CgroupV2 not used")
		}
		// use a closure to minimize the arguments, to make the usage more straightforward
		skipIfAllocatableCPUsLessThan = func(node *v1.Node, val int) {
			ginkgo.GinkgoHelper()
			checkAllocatableCPUs(node, val, reservedCPUs, onlineCPUs, smtLevel)
		}
	})

	ginkgo.AfterEach(func(ctx context.Context) {
		deletePodsAsync(ctx, f, podMap)
	})

	ginkgo.When("resizing a Guaranteed Pod with a single container and integer CPU requests", ginkgo.Label("single container guaranteed pod with integer CPU requests resize", "exclusive-cpus"), func() {
		ginkgo.BeforeEach(func(ctx context.Context) {
			reservedCPUs = cpuset.New(0)
		})

		ginkgo.DescribeTable("using a sequence of resize steps",
			func(ctx context.Context, steps ...podResizeStep) {
				originalContainers := containersForCPUs(2)
				requiredCPUs := requiredCPUsForResizeSequence(originalContainers, steps...)
				skipIfAllocatableCPUsLessThan(getLocalNode(ctx, f), requiredCPUs)
				updateKubeletConfigIfNeeded(ctx, f, configureCPUManagerInKubelet(oldCfg, &cpuManagerKubeletArguments{
					policyName:         string(cpumanager.PolicyStatic),
					reservedSystemCPUs: reservedCPUs,
					enableInPlacePodVerticalScalingExclusiveCPUs: true,
				}))
				applyPodResizeSequence(ctx, podResizeEnv{
					fwk:           f,
					createPodSync: createPodSync,
				}, originalContainers, steps...)
			},
			ginkgo.Entry("should increase exclusively allocated CPUs within available capacity",
				podResizeStep{
					desiredContainers:  containersForCPUs(3),
					expectedContainers: containersForCPUs(3),
				},
			),
			ginkgo.Entry("should increase and then decrease exclusively allocated CPUs back to the baseline",
				podResizeStep{
					desiredContainers:  containersForCPUs(3),
					expectedContainers: containersForCPUs(3),
				},
				podResizeStep{
					desiredContainers:  containersForCPUs(2),
					expectedContainers: containersForCPUs(2),
				},
			),
			ginkgo.Entry("should not decrease exclusively allocated CPUs below the baseline cpuset",
				podResizeStep{
					desiredContainers:  containersForCPUs(1),
					expectedContainers: containersForCPUs(2),
					wantError: resizeError{
						errorString: "Infeasible.*Not allowed to reduce container exclusively allocated pool below baseline.*",
						phase:       resizePhaseActuation,
					},
					terminal: true,
				},
			),
			ginkgo.Entry("should successfully resize after an infeasible resize",
				podResizeStep{
					desiredContainers:  containersForCPUs(1),
					expectedContainers: containersForCPUs(2),
					wantError: resizeError{
						errorString: "Infeasible.*Not allowed to reduce container exclusively allocated pool below baseline.*",
						phase:       resizePhaseActuation,
					},
					terminal: false,
				},
				podResizeStep{
					desiredContainers:  containersForCPUs(3),
					expectedContainers: containersForCPUs(3),
					terminal:           true,
				},
			),
		)

		ginkgo.DescribeTable("when a resize exceeds node capacity",
			func(ctx context.Context, recoverAfterFailure bool) {
				cpuCapacity, _, _ := getLocalNodeCPUDetails(ctx, f)
				originalContainers := containersForCPUs(2)
				steps := []podResizeStep{
					{
						desiredContainers:  containersForCPUs(int(cpuCapacity) + 1),
						expectedContainers: containersForCPUs(2),
						wantError: resizeError{
							errorString: ".*node didn't have enough allocatable resources.*",
							phase:       resizePhasePatch,
						},
						terminal: !recoverAfterFailure,
					},
				}
				if recoverAfterFailure {
					steps = append(steps, podResizeStep{
						desiredContainers:  containersForCPUs(3),
						expectedContainers: containersForCPUs(3),
						terminal:           true,
					})
				}
				requiredCPUs := requiredCPUsForResizeSequence(originalContainers, steps...)
				skipIfAllocatableCPUsLessThan(getLocalNode(ctx, f), requiredCPUs)
				updateKubeletConfigIfNeeded(ctx, f, configureCPUManagerInKubelet(oldCfg, &cpuManagerKubeletArguments{
					policyName:         string(cpumanager.PolicyStatic),
					reservedSystemCPUs: reservedCPUs,
					enableInPlacePodVerticalScalingExclusiveCPUs: true,
				}))
				applyPodResizeSequence(ctx, podResizeEnv{
					fwk:           f,
					createPodSync: createPodSync,
				}, originalContainers, steps...)
			},
			ginkgo.Entry("should not increase exclusively allocated CPUs beyond available capacity", false),
			ginkgo.Entry("should successfully resize after a rejected resize", true),
		)
	})

	ginkgo.When("topologyManagerPolicy is set to none, resizing a Guaranteed multiple containers Pod with integer CPU requests", ginkgo.Label("guaranteed multiple container pod with integer CPU requests resize", "exclusive-cpus"), func() {
		ginkgo.BeforeEach(func(ctx context.Context) {
			reservedCPUs = cpuset.New(0)
		})
		ginkgo.DescribeTable("",
			func(ctx context.Context,
				originalContainers []podresize.ResizableContainerInfo,
				originalCpuInfo []containerCPUInfo,
				desiredContainersFirstPatch []podresize.ResizableContainerInfo,
				expectedContainersFirstPatch []podresize.ResizableContainerInfo,
				expectedCpuInfoFirstPatch []containerCPUInfo,
				wantPatchErrorFirstPatch string,
				wantActuationErrorFirstPatch string,
				desiredContainersSecondPatch []podresize.ResizableContainerInfo,
				expectedContainersSecondPatch []podresize.ResizableContainerInfo,
				expectedCpuInfoSecondPatch []containerCPUInfo,
				wantPatchErrorSecondPatch string,
				wantActuationErrorSecondPatch string,
			) {

				expectedCPUCount := 0
				for i := range expectedCpuInfoFirstPatch {
					expectedCPUCount += expectedCpuInfoFirstPatch[i].cpuCount
				}
				skipIfAllocatableCPUsLessThan(getLocalNode(ctx, f), expectedCPUCount)

				expectedCPUCount = 0
				for i := range expectedCpuInfoSecondPatch {
					expectedCPUCount += expectedCpuInfoSecondPatch[i].cpuCount
				}
				skipIfAllocatableCPUsLessThan(getLocalNode(ctx, f), expectedCPUCount)

				updateKubeletConfigIfNeeded(ctx, f, configureCPUManagerInKubelet(oldCfg, &cpuManagerKubeletArguments{
					policyName:         string(cpumanager.PolicyStatic),
					reservedSystemCPUs: reservedCPUs, // Not really needed for the tests but helps to make a more precise check
					enableInPlacePodVerticalScalingExclusiveCPUs: true,
					topologyManagerPolicy:                        "none",
					topologyManagerScope:                         "container",
					topologyManagerPolicyOptions: map[string]string{
						"max-allowable-numa-nodes":  "8",
						"prefer-closest-numa-nodes": "true",
					},
				}))
				testPodResize(ctx, originalContainers, originalCpuInfo, desiredContainersFirstPatch, expectedContainersFirstPatch, expectedCpuInfoFirstPatch, wantPatchErrorFirstPatch, wantActuationErrorFirstPatch, desiredContainersSecondPatch, expectedContainersSecondPatch, expectedCpuInfoSecondPatch, wantPatchErrorSecondPatch, wantActuationErrorSecondPatch, podMap)
			},
			ginkgo.Entry("WORKS should first increase CPU (gu-container-1) request and limit, afterwards decrease CPU (gu-container-1) request and limit within available capacity without impacting (init-container-1)",
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						Command:   "cgrep Cpus_allowed_list /proc/self/status | cut -f2 && sleep 1d",
					},
					{
						Name:          "init-container-1",
						Resources:     &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						InitCtr:       true,
						RestartPolicy: v1.ContainerRestartPolicyNever,
						Command:       "cgrep Cpus_allowed_list /proc/self/status | cut -f2",
					},
				},
				[]containerCPUInfo{
					{
						Name:     "gu-container-1",
						cpuCount: 2,
					},
					{
						Name:     "init-container-1",
						InitCtr:  true,
						cpuCount: 2,
					},
				},
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
					{
						Name:      "init-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
					{
						Name:      "init-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]containerCPUInfo{
					{
						Name:     "gu-container-1",
						cpuCount: 4,
					},
					{
						Name:     "init-container-1",
						InitCtr:  true,
						cpuCount: 2,
					},
				},
				"",
				"",
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
					{
						Name:      "init-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
					{
						Name:      "init-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]containerCPUInfo{
					{
						Name:     "gu-container-1",
						cpuCount: 2,
					},
					{
						Name:     "init-container-1",
						InitCtr:  true,
						cpuCount: 2,
					},
				},
				"",
				"",
			),
			ginkgo.Entry("WORKS should first increase CPU (gu-container-1) request and limit, afterwards decrease CPU (gu-container-1) request and limit within available capacity without impacting sidecar",
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						Command:   "cgrep Cpus_allowed_list /proc/self/status | cut -f2 && sleep 1d",
					},
					{
						Name:          "sidecar-container-1",
						Resources:     &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						InitCtr:       true,
						RestartPolicy: v1.ContainerRestartPolicyAlways,
						Command:       "cgrep Cpus_allowed_list /proc/self/status | cut -f2 && sleep 1d",
					},
				},
				[]containerCPUInfo{
					{
						Name:     "gu-container-1",
						cpuCount: 2,
					},
					{
						Name:     "sidecar-container-1",
						cpuCount: 2,
						InitCtr:  true,
					},
				},
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
					{
						Name:          "sidecar-container-1",
						Resources:     &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						InitCtr:       true,
						RestartPolicy: v1.ContainerRestartPolicyAlways,
						Command:       "cgrep Cpus_allowed_list /proc/self/status | cut -f2 && sleep 1d",
					},
				},
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
					{
						Name:      "sidecar-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]containerCPUInfo{
					{
						Name:     "gu-container-1",
						cpuCount: 4,
					},
					{
						Name:     "sidecar-container-1",
						InitCtr:  true,
						cpuCount: 2,
					},
				},
				"",
				"",
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
					{
						Name:      "sidecar-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
					{
						Name:      "sidecar-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]containerCPUInfo{
					{
						Name:     "gu-container-1",
						cpuCount: 2,
					},
					{
						Name:     "sidecar-container-1",
						InitCtr:  true,
						cpuCount: 2,
					},
				},
				"",
				"",
			),
			ginkgo.Entry("WORKS NOT should first increase CPU (sidecar-container-1) request and limit, afterwards decrease CPU (sidecar-container-1) request and limit within available capacity",
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						Command:   "cgrep Cpus_allowed_list /proc/self/status | cut -f2 && sleep 1d",
					},
					{
						Name:          "sidecar-container-1",
						Resources:     &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						InitCtr:       true,
						RestartPolicy: v1.ContainerRestartPolicyAlways,
						Command:       "cgrep Cpus_allowed_list /proc/self/status | cut -f2 && sleep 1d",
					},
				},
				[]containerCPUInfo{
					{
						Name:     "gu-container-1",
						cpuCount: 2,
					},
					{
						Name:     "sidecar-container-1",
						cpuCount: 2,
						InitCtr:  true,
					},
				},
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
					{
						Name:          "sidecar-container-1",
						Resources:     &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						InitCtr:       true,
						RestartPolicy: v1.ContainerRestartPolicyAlways,
						Command:       "cgrep Cpus_allowed_list /proc/self/status | cut -f2 && sleep 1d",
					},
				},
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
					{
						Name:      "sidecar-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]containerCPUInfo{
					{
						Name:     "gu-container-1",
						cpuCount: 2,
					},
					{
						Name:     "sidecar-container-1",
						InitCtr:  true,
						cpuCount: 4,
					},
				},
				"",
				"",
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
					{
						Name:      "sidecar-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
					{
						Name:      "sidecar-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]containerCPUInfo{
					{
						Name:     "gu-container-1",
						cpuCount: 2,
					},
					{
						Name:     "sidecar-container-1",
						InitCtr:  true,
						cpuCount: 2,
					},
				},
				"",
				"",
			),
		)
	})

	ginkgo.When("topologyManagerPolicyOption is set to best-effort, resizing a Guaranteed multiple containers Pod with integer CPU request", ginkgo.Label("guaranteed pod with integer CPU requests resize", "exclusive-cpus"), func() {
		ginkgo.BeforeEach(func(ctx context.Context) {
			reservedCPUs = cpuset.New(0)
		})
		ginkgo.DescribeTable("",
			func(ctx context.Context, originalContainers []podresize.ResizableContainerInfo, originalCpuInfo []containerCPUInfo, desiredContainersFirstPatch []podresize.ResizableContainerInfo, expectedContainersFirstPatch []podresize.ResizableContainerInfo, expectedCpuInfoFirstPatch []containerCPUInfo, wantPatchErrorFirstPatch string, wantActuationErrorFirstPatch string, desiredContainersSecondPatch []podresize.ResizableContainerInfo, expectedContainersSecondPatch []podresize.ResizableContainerInfo, expectedCpuInfoSecondPatch []containerCPUInfo, wantPatchErrorSecondPatch string, wantActuationErrorSecondPatch string) {
				testPodResize(ctx, originalContainers, originalCpuInfo, desiredContainersFirstPatch, expectedContainersFirstPatch, expectedCpuInfoFirstPatch, wantPatchErrorFirstPatch, wantActuationErrorFirstPatch, desiredContainersSecondPatch, expectedContainersSecondPatch, expectedCpuInfoSecondPatch, wantPatchErrorSecondPatch, wantActuationErrorSecondPatch, podMap)
			},
			ginkgo.Entry("should first increase (gu-container-1) CPU request/limit, afterwards decrease (gu-container-1) CPU request/limit, within available capacity",
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]containerCPUInfo{
					{
						Name:     "gu-container-1",
						cpuCount: 2,
					},
				},
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]containerCPUInfo{
					{
						Name:     "gu-container-1",
						cpuCount: 4,
					},
				},
				"",
				"",
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]podresize.ResizableContainerInfo{
					{
						Name:      "gu-container-1",
						Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
					},
				},
				[]containerCPUInfo{
					{
						Name:     "gu-container-1",
						cpuCount: 2,
					},
				},
				"",
				"",
			),
		)
	})

	ginkgo.When("topologyManagerPolicy option is set to restricted, resizing a Guaranteed multiple containers Pod, with integer CPU request", ginkgo.Label("guaranteed multiple containers pod with integer CPU requests resize", "exclusive-cpus"), func() {
		ginkgo.BeforeEach(func(ctx context.Context) {
			if smtLevel < 1 {
				e2eskipper.Skipf("Skipping CPU Manager %q tests since SMT disabled", cpumanager.FullPCPUsOnlyOption)
			}
			reservedCPUs = cpuset.New(0)
		})
		smtLevel = smtLevelFromSysFS() // this should not change at all, at least during this suite lifetime
		framework.Logf("SMT level: %d", smtLevel)

		if smtLevel >= minSMTLevel {
			ginkgo.DescribeTable("",
				func(ctx context.Context,
					originalContainers []podresize.ResizableContainerInfo,
					originalCpuInfo []containerCPUInfo,
					desiredContainersFirstPatch []podresize.ResizableContainerInfo,
					expectedContainersFirstPatch []podresize.ResizableContainerInfo,
					expectedCpuInfoFirstPatch []containerCPUInfo,
					wantPatchErrorFirstPatch string,
					wantActuationErrorFirstPatch string,
					desiredContainersSecondPatch []podresize.ResizableContainerInfo,
					expectedContainersSecondPatch []podresize.ResizableContainerInfo,
					expectedCpuInfoSecondPatch []containerCPUInfo,
					wantPatchErrorSecondPatch string,
					wantActuationErrorSecondPatch string,
				) {

					expectedCPUCount := 0
					for i := range expectedCpuInfoFirstPatch {
						expectedCPUCount += expectedCpuInfoFirstPatch[i].cpuCount
					}
					skipIfAllocatableCPUsLessThan(getLocalNode(ctx, f), expectedCPUCount)

					expectedCPUCount = 0
					for i := range expectedCpuInfoSecondPatch {
						expectedCPUCount += expectedCpuInfoSecondPatch[i].cpuCount
					}
					skipIfAllocatableCPUsLessThan(getLocalNode(ctx, f), expectedCPUCount)

					updateKubeletConfigIfNeeded(ctx, f, configureCPUManagerInKubelet(oldCfg, &cpuManagerKubeletArguments{
						policyName:         string(cpumanager.PolicyStatic),
						reservedSystemCPUs: reservedCPUs, // Not really needed for the tests but helps to make a more precise check
						enableInPlacePodVerticalScalingExclusiveCPUs: true,
						topologyManagerPolicy:                        "restricted",
						topologyManagerScope:                         "container",
						topologyManagerPolicyOptions: map[string]string{
							"max-allowable-numa-nodes":  "8",
							"prefer-closest-numa-nodes": "true",
						},
					}))
					testPodResize(ctx, originalContainers, originalCpuInfo, desiredContainersFirstPatch, expectedContainersFirstPatch, expectedCpuInfoFirstPatch, wantPatchErrorFirstPatch, wantActuationErrorFirstPatch, desiredContainersSecondPatch, expectedContainersSecondPatch, expectedCpuInfoSecondPatch, wantPatchErrorSecondPatch, wantActuationErrorSecondPatch, podMap)
				},
				ginkgo.Entry("in a node with SMTLevel > 1, should first increase (gu-container-1) CPU request/limit, afterwards decrease (gu-container-1) CPU request/limit, within available capacity",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 2,
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "10000m", CPULim: "10000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "10000m", CPULim: "10000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 10,
						},
					},
					"",
					"",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 2,
						},
					},
					"",
					"",
				),
			)
		}
		if smtLevel == 1 {
			ginkgo.DescribeTable("",
				func(ctx context.Context,
					originalContainers []podresize.ResizableContainerInfo,
					originalCpuInfo []containerCPUInfo,
					desiredContainersFirstPatch []podresize.ResizableContainerInfo,
					expectedContainersFirstPatch []podresize.ResizableContainerInfo,
					expectedCpuInfoFirstPatch []containerCPUInfo,
					wantPatchErrorFirstPatch string,
					wantActuationErrorFirstPatch string,
					desiredContainersSecondPatch []podresize.ResizableContainerInfo,
					expectedContainersSecondPatch []podresize.ResizableContainerInfo,
					expectedCpuInfoSecondPatch []containerCPUInfo,
					wantPatchErrorSecondPatch string,
					wantActuationErrorSecondPatch string,
				) {

					expectedCPUCount := 0
					for i := range expectedCpuInfoFirstPatch {
						expectedCPUCount += expectedCpuInfoFirstPatch[i].cpuCount
					}
					skipIfAllocatableCPUsLessThan(getLocalNode(ctx, f), expectedCPUCount)

					expectedCPUCount = 0
					for i := range expectedCpuInfoSecondPatch {
						expectedCPUCount += expectedCpuInfoSecondPatch[i].cpuCount
					}
					skipIfAllocatableCPUsLessThan(getLocalNode(ctx, f), expectedCPUCount)

					updateKubeletConfigIfNeeded(ctx, f, configureCPUManagerInKubelet(oldCfg, &cpuManagerKubeletArguments{
						policyName:         string(cpumanager.PolicyStatic),
						reservedSystemCPUs: reservedCPUs, // Not really needed for the tests but helps to make a more precise check
						enableInPlacePodVerticalScalingExclusiveCPUs: true,
						topologyManagerPolicy:                        "restricted",
						topologyManagerScope:                         "container",
						topologyManagerPolicyOptions: map[string]string{
							"max-allowable-numa-nodes":  "8",
							"prefer-closest-numa-nodes": "true",
						},
					}))
					testPodResize(ctx, originalContainers, originalCpuInfo, desiredContainersFirstPatch, expectedContainersFirstPatch, expectedCpuInfoFirstPatch, wantPatchErrorFirstPatch, wantActuationErrorFirstPatch, desiredContainersSecondPatch, expectedContainersSecondPatch, expectedCpuInfoSecondPatch, wantPatchErrorSecondPatch, wantActuationErrorSecondPatch, podMap)
				},
				ginkgo.Entry("in a node with SMTLevel = 1, should first increase (gu-container-1) CPU request/limit, afterwards fail to decrease (gu-container-1) CPU request/limit, within available capacity because of TopologyAffinityError, in a node with SMTLevel = 1 ",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 2,
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "10000m", CPULim: "10000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "10000m", CPULim: "10000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 10,
						},
					},
					"",
					"",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "10000m", CPULim: "10000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 10,
						},
					},
					"",
					"Infeasible.*",
				),
			)
		}
	})

	ginkgo.When("topology manager policy option is set to single-numa-node, resizing a Guaranteed multiple container pod, with integer CPU request", ginkgo.Label("guaranteed multiple containers pod with integer CPU requests resize", "exclusive-cpus"), func() {
		ginkgo.BeforeEach(func(ctx context.Context) {
			if smtLevel < 1 {
				e2eskipper.Skipf("Skipping CPU Manager %q tests since SMT disabled", cpumanager.FullPCPUsOnlyOption)
			}
			reservedCPUs = cpuset.New(0)
		})
		smtLevel = smtLevelFromSysFS() // this should not change at all, at least during this suite lifetime
		framework.Logf("SMT level: %d", smtLevel)

		if smtLevel >= minSMTLevel {
			ginkgo.DescribeTable("",
				func(ctx context.Context,
					originalContainers []podresize.ResizableContainerInfo,
					originalCpuInfo []containerCPUInfo,
					desiredContainersFirstPatch []podresize.ResizableContainerInfo,
					expectedContainersFirstPatch []podresize.ResizableContainerInfo,
					expectedCpuInfoFirstPatch []containerCPUInfo,
					wantPatchErrorFirstPatch string,
					wantActuationErrorFirstPatch string,
					desiredContainersSecondPatch []podresize.ResizableContainerInfo,
					expectedContainersSecondPatch []podresize.ResizableContainerInfo,
					expectedCpuInfoSecondPatch []containerCPUInfo,
					wantPatchErrorSecondPatch string,
					wantActuationErrorSecondPatch string,
				) {

					expectedCPUCount := 0
					for i := range expectedCpuInfoFirstPatch {
						expectedCPUCount += expectedCpuInfoFirstPatch[i].cpuCount
					}
					skipIfAllocatableCPUsLessThan(getLocalNode(ctx, f), expectedCPUCount)

					expectedCPUCount = 0
					for i := range expectedCpuInfoSecondPatch {
						expectedCPUCount += expectedCpuInfoSecondPatch[i].cpuCount
					}
					skipIfAllocatableCPUsLessThan(getLocalNode(ctx, f), expectedCPUCount)

					updateKubeletConfigIfNeeded(ctx, f, configureCPUManagerInKubelet(oldCfg, &cpuManagerKubeletArguments{
						policyName:         string(cpumanager.PolicyStatic),
						reservedSystemCPUs: reservedCPUs, // Not really needed for the tests but helps to make a more precise check
						enableInPlacePodVerticalScalingExclusiveCPUs: true,
						topologyManagerPolicy:                        "single-numa-node",
						topologyManagerScope:                         "container",
						topologyManagerPolicyOptions: map[string]string{
							"max-allowable-numa-nodes":  "8",
							"prefer-closest-numa-nodes": "true",
						},
					}))
					testPodResize(ctx, originalContainers, originalCpuInfo, desiredContainersFirstPatch, expectedContainersFirstPatch, expectedCpuInfoFirstPatch, wantPatchErrorFirstPatch, wantActuationErrorFirstPatch, desiredContainersSecondPatch, expectedContainersSecondPatch, expectedCpuInfoSecondPatch, wantPatchErrorSecondPatch, wantActuationErrorSecondPatch, podMap)
				},
				ginkgo.Entry("in a node with SMTLevel > 1, should first increase (gu-container-1) CPU request/limit and afterwards decrease (gu-container-1) CPU request/limit, within available capacity",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 2,
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 4,
						},
					},
					"",
					"",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 2,
						},
					},
					"",
					"",
				),
				ginkgo.Entry("in a node with SMTLevel > 1, should first increase (gu-container-1) CPU request/limit, afterwards fail to reduce (gu-container-1) CPU request/limit, below baseline and increase (gu-container-2) CPU request/limit, within available capacity",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "3000m", CPULim: "3000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 3,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 4,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					"",
					"",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 4,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					"",
					"Infeasible.*Not allowed to reduce container exclusively allocated pool below baseline.*",
				),
				ginkgo.Entry("in a node with SMTLevel > 1, should first increase (gu-container-1) CPU request/limit, afterwards restore (gu-container-1) CPU request/limit and increase (gu-container-2) CPU request/limit, within available capacity",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 2,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 4,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					"",
					"",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 2,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 4,
						},
					},
					"",
					"",
				),
				ginkgo.Entry("in a node with SMTLevel > 1, should first increase (gu-container-1) CPU request/limit, afterwards fail to reduce (gu-container-1) CPU request/limit to baseline and increase (gu-container-2) CPU request/limit, beyond available capacity",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "3000m", CPULim: "3000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 3,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 4,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					"",
					"",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "3000m", CPULim: "3000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "40000m", CPULim: "40000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 4,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					".*node didn't have enough allocatable resources.*",
					"",
				),
			)
		}
		if smtLevel == 1 {
			ginkgo.DescribeTable("",
				func(ctx context.Context,
					originalContainers []podresize.ResizableContainerInfo,
					originalCpuInfo []containerCPUInfo,
					desiredContainersFirstPatch []podresize.ResizableContainerInfo,
					expectedContainersFirstPatch []podresize.ResizableContainerInfo,
					expectedCpuInfoFirstPatch []containerCPUInfo,
					wantPatchErrorFirstPatch string,
					wantActuationErrorFirstPatch string,
					desiredContainersSecondPatch []podresize.ResizableContainerInfo,
					expectedContainersSecondPatch []podresize.ResizableContainerInfo,
					expectedCpuInfoSecondPatch []containerCPUInfo,
					wantPatchErrorSecondPatch string,
					wantActuationErrorSecondPatch string,
				) {

					expectedCPUCount := 0
					for i := range expectedCpuInfoFirstPatch {
						expectedCPUCount += expectedCpuInfoFirstPatch[i].cpuCount
					}
					skipIfAllocatableCPUsLessThan(getLocalNode(ctx, f), expectedCPUCount)

					expectedCPUCount = 0
					for i := range expectedCpuInfoSecondPatch {
						expectedCPUCount += expectedCpuInfoSecondPatch[i].cpuCount
					}
					skipIfAllocatableCPUsLessThan(getLocalNode(ctx, f), expectedCPUCount)

					updateKubeletConfigIfNeeded(ctx, f, configureCPUManagerInKubelet(oldCfg, &cpuManagerKubeletArguments{
						policyName:         string(cpumanager.PolicyStatic),
						reservedSystemCPUs: reservedCPUs, // Not really needed for the tests but helps to make a more precise check
						enableInPlacePodVerticalScalingExclusiveCPUs: true,
						topologyManagerPolicy:                        "single-numa-node",
						topologyManagerScope:                         "container",
						topologyManagerPolicyOptions: map[string]string{
							"max-allowable-numa-nodes":  "8",
							"prefer-closest-numa-nodes": "true",
						},
					}))
					testPodResize(ctx, originalContainers, originalCpuInfo, desiredContainersFirstPatch, expectedContainersFirstPatch, expectedCpuInfoFirstPatch, wantPatchErrorFirstPatch, wantActuationErrorFirstPatch, desiredContainersSecondPatch, expectedContainersSecondPatch, expectedCpuInfoSecondPatch, wantPatchErrorSecondPatch, wantActuationErrorSecondPatch, podMap)
				},
				ginkgo.Entry("in a node with SMTLevel = 1, should first increase (gu-container-1) CPU request/limit and afterwards decrease (gu-container-1) CPU request/limit, within available capacity",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 2,
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 4,
						},
					},
					"",
					"",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 2,
						},
					},
					"",
					"",
				),
				ginkgo.Entry("in a node with SMTLevel = 1, should first increase (gu-container-1) CPU request/limit, afterwards restore (gu-container-1) CPU request/limit and increase (gu-container-2) CPU request/limit, within available capacity",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 2,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 4,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					"",
					"",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 2,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 4,
						},
					},
					"",
					"",
				),
				ginkgo.Entry("in a node with SMTLevel = 1, should first increase (gu-container-1) CPU request/limit, afterwards fail to reduce (gu-container-1) CPU request/limit, below original and increase (gu-container-2) CPU request/limit, within available capacity",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "3000m", CPULim: "3000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 3,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 4,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					"",
					"",
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]podresize.ResizableContainerInfo{
						{
							Name:      "gu-container-1",
							Resources: &cgroups.ContainerResources{CPUReq: "4000m", CPULim: "4000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
						{
							Name:      "gu-container-2",
							Resources: &cgroups.ContainerResources{CPUReq: "2000m", CPULim: "2000m", MemReq: "200Mi", MemLim: "200Mi"},
						},
					},
					[]containerCPUInfo{
						{
							Name:     "gu-container-1",
							cpuCount: 4,
						},
						{
							Name:     "gu-container-2",
							cpuCount: 2,
						},
					},
					"",
					"Infeasible.*Not allowed to reduce container exclusively allocated pool below baseline.*",
				),
			)
		}
	})
},
)

func checkAllocatedCPUsForResize(ctx context.Context, f *framework.Framework, expectedCpusInfo []containerCPUInfo, testPod *v1.Pod) {
	for i := range expectedCpusInfo {
		if expectedCpusInfo[i].InitCtr {
			// when we get there the real initcontainer terminated, so we can only check its logs
			ginkgo.By("checking if the expected cpuset was assigned")
			logs, err := e2epod.GetPodLogs(ctx, f.ClientSet, testPod.Namespace, testPod.Name, expectedCpusInfo[i].Name)
			framework.ExpectNoError(err, "expected log not found in init container [%s] of pod [%s]", expectedCpusInfo[i].Name, testPod.Name)
			gomega.Expect(testPod).To(HaveInitContainerCPUsCount(expectedCpusInfo[i].Name, expectedCpusInfo[i].cpuCount, logs))
		} else {
			gomega.Expect(testPod).To(HaveContainerCPUsCount(expectedCpusInfo[i].Name, expectedCpusInfo[i].cpuCount))
		}
	}
}
