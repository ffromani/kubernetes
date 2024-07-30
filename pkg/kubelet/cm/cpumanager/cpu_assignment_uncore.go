/*
Copyright 2024 The Kubernetes Authors.

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

package cpumanager

import (
	"sort"

	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"
)

// Sorts the provided list of NUMA nodes/sockets/cores/cpus referenced in 'ids'
// by the number of available CPUs contained within them (smallest to largest).
// The 'getCPU()' parameter defines the function that should be called to
// retrieve the list of available CPUs for the type being referenced. If two
// NUMA nodes/sockets/cores/cpus have the same number of available CPUs, they
// are sorted in ascending order by their id.
func (a *cpuAccumulator) sortBySize(ids []int, getCPUCount func(ids ...int) int) {
	sort.Slice(ids,
		func(i, j int) bool {
			iCount := getCPUCount(ids[i])
			jCount := getCPUCount(ids[j])
			if iCount < jCount {
				return true
			}
			if iCount > jCount {
				return false
			}
			return ids[i] < ids[j]
		})
}

func (n *numaFirst) sortAvailableCoresForUncoreCaches() []int {
	var result []int
	for _, cache := range n.acc.sortAvailableUncoreCaches() {
		cores := n.acc.details.CoresInUncoreCaches(cache).UnsortedList()
		n.acc.sort(cores, n.acc.details.CPUsInCores)
		result = append(result, cores...)
	}
	return result
}

// note this sorts in reverse, from the less to the most full. This can cause fragmentation
func (n *numaFirst) sortAvailableUncoreCaches() []int {
	uncoreCpus := n.acc.topo.CPUsPerUncoreCache()
	var result []int
	for _, socket := range n.sortAvailableSockets() {
		caches := n.acc.details.UncoreCachesInSocket(socket).UnsortedList()
		n.acc.sortBySize(caches, func(ids ...int) int {
			freeCpus := n.acc.details.CPUsInUncoreCaches(ids[0]).Size()
			return uncoreCpus - freeCpus
		})
		result = append(result, caches...)
	}
	return result
}

func (s *socketsFirst) sortAvaliableCoresForUncoreCaches() []int {
	var result []int
	for _, cache := range s.acc.sortAvailableUncoreCaches() {
		cores := s.acc.details.CoresInUncoreCaches(cache).UnsortedList()
		s.acc.sort(cores, s.acc.details.CPUsInCores)
		result = append(result, cores...)
	}
	return result
}

func (s *socketsFirst) sortAvailableUncoreCaches() []int {
	uncoreCpus := s.acc.topo.CPUsPerUncoreCache()
	var result []int
	for _, node := range s.sortAvailableNUMANodes() {
		caches := s.acc.details.UncoreCachesInNUMANode(node).UnsortedList()
		s.acc.sortBySize(caches, func(ids ...int) int {
			freeCpus := s.acc.details.CPUsInUncoreCaches(ids[0]).Size()
			return uncoreCpus - freeCpus
		})
		result = append(result, caches...)
	}
	return result
}

func (a *cpuAccumulator) takeFullUncoreGroups() {
	for _, uncorecache := range a.freeUncoreCaches() {
		cpusInUncoreCache := a.topo.CPUDetails.CPUsInUncoreCaches(uncorecache)
		if !a.needsAtLeast(cpusInUncoreCache.Size()) {
			continue
		}
		klog.V(4).InfoS("takeFullUncoreCaches: claiming uncore-cache", "uncore-cache", uncorecache)
		a.take(cpusInUncoreCache)
	}
	return
}

// Returns true if the supplied core is fully available in `topoDetails`.
func (a *cpuAccumulator) isUncoreCacheFree(uncoreCacheID int) bool {
	return a.details.CPUsInUncoreCaches(uncoreCacheID).Size() == a.topo.CPUsPerUncoreCache()
}

func (a *cpuAccumulator) sortAvailableUncoreCaches() []int {
	return a.numaOrSocketsFirst.sortAvailableUncoreCaches()
}

// Returns free uncore cache IDs as a slice sorted by sortAvailableUncoreCaches().
func (a *cpuAccumulator) freeUncoreCaches() []int {
	free := []int{}
	for _, cache := range a.sortAvailableUncoreCaches() {
		if a.isUncoreCacheFree(cache) {
			free = append(free, cache)
		}
	}
	return free
}

func SetupTopologyByPolicyOptions(topology *topology.CPUTopology, opts StaticPolicyOptions) *topology.CPUTopology {
	if opts.AlignByUnCoreCache {
		return topology // nothing to do, happy as we are already, just consume the data
	}
	topology = topology.DeepCopy()
	topology.NumUnCoreCaches = 0 // abuse the flag to disable logic deep down in takeByTopology
	klog.InfoS("Static policy uncore cache", "count", topology.NumUnCoreCaches, "aligning", opts.AlignByUnCoreCache)
	return topology
}
