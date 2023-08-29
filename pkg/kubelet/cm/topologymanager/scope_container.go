/*
Copyright 2020 The Kubernetes Authors.

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

package topologymanager

import (
	"k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/cm/admission"
	"k8s.io/kubernetes/pkg/kubelet/cm/containermap"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/metrics"
)

type containerScope struct {
	scope
}

// Ensure containerScope implements Scope interface
var _ Scope = &containerScope{}

// NewContainerScope returns a container scope.
func NewContainerScope(policy Policy) Scope {
	return &containerScope{
		scope{
			name:             containerTopologyScope,
			podTopologyHints: podTopologyHints{},
			policy:           policy,
			podMap:           containermap.NewContainerMap(),
		},
	}
}

func (s *containerScope) Admit(pod *v1.Pod) lifecycle.PodAdmitResult {
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		bestHints, admit := s.calculateAffinity(pod, &container)

		if !admit {
			metrics.TopologyManagerAdmissionErrorsTotal.Inc()
			return admission.GetPodAdmitResult(&TopologyAffinityError{})
		}

		for resourceName, bestHint := range bestHints {
			klog.InfoS("Topology Affinity", "bestHint", bestHint, "pod", klog.KObj(pod), "containerName", container.Name, "resource", resourceName)
			s.setTopologyHints(string(pod.UID), container.Name, resourceName, bestHint)
		}

		err := s.allocateAlignedResources(pod, &container)
		if err != nil {
			metrics.TopologyManagerAdmissionErrorsTotal.Inc()
			return admission.GetPodAdmitResult(err)
		}
	}
	return admission.GetPodAdmitResult(nil)
}

func (s *containerScope) accumulateProvidersHints(pod *v1.Pod, container *v1.Container) []map[string][]TopologyHint {
	var providersHints []map[string][]TopologyHint

	for _, provider := range s.hintProviders {
		// Get the TopologyHints for a Container from a provider.
		hints := provider.GetTopologyHints(pod, container)
		providersHints = append(providersHints, hints)
		klog.InfoS("TopologyHints", "hints", hints, "pod", klog.KObj(pod), "containerName", container.Name)
	}
	return providersHints
}

func (s *containerScope) calculateAffinity(pod *v1.Pod, container *v1.Container) (map[string]TopologyHint, bool) {
	providersHints := s.accumulateProvidersHints(pod, container)
	bestHints, admit := s.policy.Merge(string(pod.UID), container.Name, container.Resources.Properties, providersHints)
	klog.InfoS("ContainerTopologyHint", "bestHints", bestHints) // TODO pretty print
	return bestHints, admit
}
