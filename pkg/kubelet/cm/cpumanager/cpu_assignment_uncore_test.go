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
	"fmt"
	"reflect"
	"strings"
	"testing"

	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"
	"k8s.io/utils/cpuset"
)

var (
	// Intel(R) Xeon(R) Gold 5218 CPU @ 2.30GHz
	gold_5218_topology = &topology.CPUTopology{
		NumCPUs:    64,
		NumSockets: 2,
		NumCores:   16,
		CPUDetails: map[int]topology.CPUInfo{
			0:  {CoreID: 0, SocketID: 0, UnCoreCacheID: 0},
			1:  {CoreID: 0, SocketID: 1, UnCoreCacheID: 0},
			2:  {CoreID: 7, SocketID: 0, UnCoreCacheID: 0},
			3:  {CoreID: 7, SocketID: 1, UnCoreCacheID: 0},
			4:  {CoreID: 1, SocketID: 0, UnCoreCacheID: 0},
			5:  {CoreID: 1, SocketID: 1, UnCoreCacheID: 0},
			6:  {CoreID: 6, SocketID: 0, UnCoreCacheID: 0},
			7:  {CoreID: 6, SocketID: 1, UnCoreCacheID: 0},
			8:  {CoreID: 2, SocketID: 0, UnCoreCacheID: 0},
			9:  {CoreID: 2, SocketID: 1, UnCoreCacheID: 0},
			10: {CoreID: 5, SocketID: 0, UnCoreCacheID: 0},
			11: {CoreID: 5, SocketID: 1, UnCoreCacheID: 0},
			12: {CoreID: 3, SocketID: 0, UnCoreCacheID: 0},
			13: {CoreID: 3, SocketID: 1, UnCoreCacheID: 0},
			14: {CoreID: 4, SocketID: 0, UnCoreCacheID: 0},
			15: {CoreID: 4, SocketID: 1, UnCoreCacheID: 0},
			16: {CoreID: 8, SocketID: 0, UnCoreCacheID: 0},
			17: {CoreID: 8, SocketID: 1, UnCoreCacheID: 0},
			18: {CoreID: 15, SocketID: 0, UnCoreCacheID: 0},
			19: {CoreID: 15, SocketID: 1, UnCoreCacheID: 0},
			20: {CoreID: 9, SocketID: 0, UnCoreCacheID: 0},
			21: {CoreID: 9, SocketID: 1, UnCoreCacheID: 0},
			22: {CoreID: 14, SocketID: 0, UnCoreCacheID: 0},
			23: {CoreID: 14, SocketID: 1, UnCoreCacheID: 0},
			24: {CoreID: 10, SocketID: 0, UnCoreCacheID: 0},
			25: {CoreID: 10, SocketID: 1, UnCoreCacheID: 0},
			26: {CoreID: 13, SocketID: 0, UnCoreCacheID: 0},
			27: {CoreID: 13, SocketID: 1, UnCoreCacheID: 0},
			28: {CoreID: 11, SocketID: 0, UnCoreCacheID: 0},
			29: {CoreID: 11, SocketID: 1, UnCoreCacheID: 0},
			30: {CoreID: 12, SocketID: 0, UnCoreCacheID: 0},
			31: {CoreID: 12, SocketID: 1, UnCoreCacheID: 0},
			32: {CoreID: 0, SocketID: 0, UnCoreCacheID: 1},
			33: {CoreID: 0, SocketID: 1, UnCoreCacheID: 1},
			34: {CoreID: 7, SocketID: 0, UnCoreCacheID: 1},
			35: {CoreID: 7, SocketID: 1, UnCoreCacheID: 1},
			36: {CoreID: 1, SocketID: 0, UnCoreCacheID: 1},
			37: {CoreID: 1, SocketID: 1, UnCoreCacheID: 1},
			38: {CoreID: 6, SocketID: 0, UnCoreCacheID: 1},
			39: {CoreID: 6, SocketID: 1, UnCoreCacheID: 1},
			40: {CoreID: 2, SocketID: 0, UnCoreCacheID: 1},
			41: {CoreID: 2, SocketID: 1, UnCoreCacheID: 1},
			42: {CoreID: 5, SocketID: 0, UnCoreCacheID: 1},
			43: {CoreID: 5, SocketID: 1, UnCoreCacheID: 1},
			44: {CoreID: 3, SocketID: 0, UnCoreCacheID: 1},
			45: {CoreID: 3, SocketID: 1, UnCoreCacheID: 1},
			46: {CoreID: 4, SocketID: 0, UnCoreCacheID: 1},
			47: {CoreID: 4, SocketID: 1, UnCoreCacheID: 1},
			48: {CoreID: 8, SocketID: 0, UnCoreCacheID: 1},
			49: {CoreID: 8, SocketID: 1, UnCoreCacheID: 1},
			50: {CoreID: 15, SocketID: 0, UnCoreCacheID: 1},
			51: {CoreID: 15, SocketID: 1, UnCoreCacheID: 1},
			52: {CoreID: 9, SocketID: 0, UnCoreCacheID: 1},
			53: {CoreID: 9, SocketID: 1, UnCoreCacheID: 1},
			54: {CoreID: 14, SocketID: 0, UnCoreCacheID: 1},
			55: {CoreID: 14, SocketID: 1, UnCoreCacheID: 1},
			56: {CoreID: 10, SocketID: 0, UnCoreCacheID: 1},
			57: {CoreID: 10, SocketID: 1, UnCoreCacheID: 1},
			58: {CoreID: 13, SocketID: 0, UnCoreCacheID: 1},
			59: {CoreID: 13, SocketID: 1, UnCoreCacheID: 1},
			60: {CoreID: 11, SocketID: 0, UnCoreCacheID: 1},
			61: {CoreID: 11, SocketID: 1, UnCoreCacheID: 1},
			62: {CoreID: 12, SocketID: 0, UnCoreCacheID: 1},
			63: {CoreID: 12, SocketID: 1, UnCoreCacheID: 1},
		},
	}

	AMD_EPYC_7502P_32_Core_Processor = &topology.CPUTopology{
		NumCPUs:      64,
		NumCores:     32,
		NumSockets:   1,
		NumNUMANodes: 1,
		CPUDetails: map[int]topology.CPUInfo{
			0:  {CoreID: 0, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			32: {CoreID: 0, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			1:  {CoreID: 1, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			33: {CoreID: 1, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			2:  {CoreID: 2, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			34: {CoreID: 2, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			3:  {CoreID: 3, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			35: {CoreID: 3, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			4:  {CoreID: 4, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 1},
			36: {CoreID: 4, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 1},
			5:  {CoreID: 5, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 1},
			37: {CoreID: 5, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 1},
			6:  {CoreID: 6, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 1},
			38: {CoreID: 6, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 1},
			7:  {CoreID: 7, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 1},
			39: {CoreID: 7, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 1},
			8:  {CoreID: 8, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 2},
			40: {CoreID: 8, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 2},
			9:  {CoreID: 9, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 2},
			41: {CoreID: 9, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 2},
			10: {CoreID: 10, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 2},
			42: {CoreID: 10, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 2},
			11: {CoreID: 11, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 2},
			43: {CoreID: 11, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 2},
			12: {CoreID: 12, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 3},
			44: {CoreID: 12, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 3},
			13: {CoreID: 13, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 3},
			45: {CoreID: 13, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 3},
			14: {CoreID: 14, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 3},
			46: {CoreID: 14, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 3},
			15: {CoreID: 15, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 3},
			47: {CoreID: 15, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 3},
			16: {CoreID: 16, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 4},
			48: {CoreID: 16, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 4},
			17: {CoreID: 17, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 4},
			49: {CoreID: 17, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 4},
			18: {CoreID: 18, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 4},
			50: {CoreID: 18, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 4},
			19: {CoreID: 19, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 4},
			51: {CoreID: 19, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 4},
			20: {CoreID: 20, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 5},
			52: {CoreID: 20, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 5},
			21: {CoreID: 21, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 5},
			53: {CoreID: 21, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 5},
			22: {CoreID: 22, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 5},
			54: {CoreID: 22, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 5},
			23: {CoreID: 23, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 5},
			55: {CoreID: 23, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 5},
			24: {CoreID: 24, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 6},
			56: {CoreID: 24, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 6},
			25: {CoreID: 25, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 6},
			57: {CoreID: 25, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 6},
			26: {CoreID: 26, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 6},
			58: {CoreID: 26, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 6},
			27: {CoreID: 27, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 6},
			59: {CoreID: 27, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 6},
			28: {CoreID: 28, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 7},
			60: {CoreID: 28, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 7},
			29: {CoreID: 29, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 7},
			61: {CoreID: 29, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 7},
			30: {CoreID: 30, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 7},
			62: {CoreID: 30, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 7},
			31: {CoreID: 31, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 7},
			63: {CoreID: 31, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 7},
		},
	}

	Intel_R__Xeon_R__Gold_5120_CPU___2_20GHz = &topology.CPUTopology{
		NumCPUs:      56,
		NumCores:     28,
		NumSockets:   2,
		NumNUMANodes: 2,
		CPUDetails: map[int]topology.CPUInfo{
			0:  {CoreID: 0, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			28: {CoreID: 0, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			2:  {CoreID: 1, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			30: {CoreID: 1, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			4:  {CoreID: 2, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			32: {CoreID: 2, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			6:  {CoreID: 3, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			34: {CoreID: 3, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			8:  {CoreID: 4, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			36: {CoreID: 4, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			10: {CoreID: 5, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			38: {CoreID: 5, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			12: {CoreID: 6, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			40: {CoreID: 6, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			14: {CoreID: 7, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			42: {CoreID: 7, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			16: {CoreID: 8, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			44: {CoreID: 8, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			18: {CoreID: 9, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			46: {CoreID: 9, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			20: {CoreID: 10, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			48: {CoreID: 10, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			22: {CoreID: 11, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			50: {CoreID: 11, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			24: {CoreID: 12, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			52: {CoreID: 12, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			26: {CoreID: 13, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			54: {CoreID: 13, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
			1:  {CoreID: 14, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			29: {CoreID: 14, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			3:  {CoreID: 15, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			31: {CoreID: 15, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			5:  {CoreID: 16, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			33: {CoreID: 16, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			7:  {CoreID: 17, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			35: {CoreID: 17, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			9:  {CoreID: 18, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			37: {CoreID: 18, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			11: {CoreID: 19, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			39: {CoreID: 19, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			13: {CoreID: 20, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			41: {CoreID: 20, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			15: {CoreID: 21, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			43: {CoreID: 21, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			17: {CoreID: 22, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			45: {CoreID: 22, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			19: {CoreID: 23, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			47: {CoreID: 23, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			21: {CoreID: 24, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			49: {CoreID: 24, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			23: {CoreID: 25, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			51: {CoreID: 25, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			25: {CoreID: 26, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			53: {CoreID: 26, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			27: {CoreID: 27, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
			55: {CoreID: 27, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: 1},
		},
	}

	topoDualUncoreCacheSingleSocketHT = &topology.CPUTopology{
		NumCPUs:         16,
		NumSockets:      1,
		NumCores:        8,
		NumUnCoreCaches: 2,
		CPUDetails: map[int]topology.CPUInfo{
			0:  {CoreID: 0, SocketID: 0, UnCoreCacheID: 0},
			1:  {CoreID: 0, SocketID: 0, UnCoreCacheID: 0},
			2:  {CoreID: 1, SocketID: 0, UnCoreCacheID: 0},
			3:  {CoreID: 1, SocketID: 0, UnCoreCacheID: 0},
			4:  {CoreID: 2, SocketID: 0, UnCoreCacheID: 0},
			5:  {CoreID: 2, SocketID: 0, UnCoreCacheID: 0},
			6:  {CoreID: 3, SocketID: 0, UnCoreCacheID: 0},
			7:  {CoreID: 3, SocketID: 0, UnCoreCacheID: 0},
			8:  {CoreID: 4, SocketID: 0, UnCoreCacheID: 1},
			9:  {CoreID: 4, SocketID: 0, UnCoreCacheID: 1},
			10: {CoreID: 5, SocketID: 0, UnCoreCacheID: 1},
			11: {CoreID: 5, SocketID: 0, UnCoreCacheID: 1},
			12: {CoreID: 6, SocketID: 0, UnCoreCacheID: 1},
			13: {CoreID: 6, SocketID: 0, UnCoreCacheID: 1},
			14: {CoreID: 7, SocketID: 0, UnCoreCacheID: 1},
			15: {CoreID: 7, SocketID: 0, UnCoreCacheID: 1},
		},
	}
)

func TestCPUAccumulatorFreeCoresUncoreCacheEnabled(t *testing.T) {
	testCases := []struct {
		description   string
		topo          *topology.CPUTopology
		availableCPUs cpuset.CPUSet
		expect        []int
	}{
		{
			"single socket HT, 4 cores free",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7),
			[]int{0, 1, 2, 3},
		},
		{
			"single socket HT, 3 cores free",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 4, 5, 6),
			[]int{0, 1, 2},
		},
		{
			"single socket HT, 3 cores free (1 partially consumed)",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6),
			[]int{0, 1, 2},
		},
		{
			"single socket HT, 0 cores free",
			topoSingleSocketHT,
			cpuset.New(),
			[]int{},
		},
		{
			"single socket HT, 0 cores free (4 partially consumed)",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3),
			[]int{},
		},
		{
			"dual socket HT, 6 cores free",
			topoDualSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11),
			[]int{0, 2, 4, 1, 3, 5},
		},
		{
			"dual socket HT, 5 cores free (1 consumed from socket 0)",
			topoDualSocketHT,
			cpuset.New(2, 1, 3, 4, 5, 7, 8, 9, 10, 11),
			[]int{2, 4, 1, 3, 5},
		},
		{
			"dual socket HT, 5 cores free (1 consumed from socket 1)",
			topoDualSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 6, 7, 8, 9, 10),
			[]int{1, 3, 0, 2, 4},
		},
		{
			"dual socket HT, 4 cores free (1 consumed from each socket)",
			topoDualSocketHT,
			cpuset.New(2, 3, 4, 5, 8, 9, 10, 11),
			[]int{2, 4, 3, 5},
		},
	}
	for _, tc := range testCases {
		acc := newCPUAccumulator(tc.topo, tc.availableCPUs, 0)
		result := acc.freeCores()
		if !reflect.DeepEqual(result, tc.expect) {
			t.Errorf("[%s] expected %v to equal %v", tc.description, result, tc.expect)
		}
	}
}

func TestCPUAccumulatorFreeCPUsUncoreCacheEnabled(t *testing.T) {
	testCases := []struct {
		description   string
		topo          *topology.CPUTopology
		availableCPUs cpuset.CPUSet
		expect        []int
	}{
		{
			"single socket HT, 8 cpus free",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7),
			[]int{0, 4, 1, 5, 2, 6, 3, 7},
		},
		{
			"single socket HT, 5 cpus free",
			topoSingleSocketHT,
			cpuset.New(3, 4, 5, 6, 7),
			[]int{4, 5, 6, 3, 7},
		},
		{
			"dual socket HT, 12 cpus free",
			topoDualSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11),
			[]int{0, 6, 2, 8, 4, 10, 1, 7, 3, 9, 5, 11},
		},
		{
			"dual socket HT, 11 cpus free",
			topoDualSocketHT,
			cpuset.New(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11),
			[]int{6, 2, 8, 4, 10, 1, 7, 3, 9, 5, 11},
		},
		{
			"dual socket HT, 10 cpus free",
			topoDualSocketHT,
			cpuset.New(1, 2, 3, 4, 5, 7, 8, 9, 10, 11),
			[]int{2, 8, 4, 10, 1, 7, 3, 9, 5, 11},
		},
		{
			"dual socket HT, 10 cpus free",
			topoDualSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 6, 7, 8, 9, 10),
			[]int{1, 7, 3, 9, 0, 6, 2, 8, 4, 10},
		},
		{
			"triple socket HT, 12 cpus free",
			topoTripleSocketHT,
			cpuset.New(0, 1, 2, 3, 6, 7, 8, 9, 10, 11, 12, 13),
			[]int{12, 13, 0, 1, 2, 3, 6, 7, 8, 9, 10, 11},
		},
	}
	for _, tc := range testCases {
		acc := newCPUAccumulator(tc.topo, tc.availableCPUs, 0)
		result := acc.freeCPUs()
		if !reflect.DeepEqual(result, tc.expect) {
			t.Errorf("[%s] expected %v to equal %v", tc.description, result, tc.expect)
		}
	}
}

func TestTakeByTopologyUncoreCacheEnabled(t *testing.T) {
	testCases := []struct {
		description   string
		topo          *topology.CPUTopology
		availableCPUs cpuset.CPUSet
		numCPUs       int
		expErr        string
		expResult     cpuset.CPUSet
	}{
		{
			"take more cpus than are available from single socket with HT",
			topoSingleSocketHT,
			cpuset.New(0, 2, 4, 6),
			5,
			"not enough cpus available to satisfy request: requested=5, available=4",
			cpuset.New(),
		},
		{
			"take zero cpus from single socket with HT",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7),
			0,
			"",
			cpuset.New(),
		},
		{
			"take one cpu from single socket with HT",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7),
			1,
			"",
			cpuset.New(0),
		},
		{
			"take one cpu from single socket with HT, some cpus are taken",
			topoSingleSocketHT,
			cpuset.New(1, 3, 5, 6, 7),
			1,
			"",
			cpuset.New(6),
		},
		{
			"take two cpus from single socket with HT",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7),
			2,
			"",
			cpuset.New(0, 4),
		},
		{
			"take all cpus from single socket with HT",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7),
			8,
			"",
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7),
		},
		{
			"take two cpus from single socket with HT, only one core totally free",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 6),
			2,
			"",
			cpuset.New(2, 6),
		},
		{
			"take one cpu from dual socket with HT - core from Socket 0",
			topoDualSocketHT,
			cpuset.New(1, 2, 3, 4, 5, 7, 8, 9, 10, 11),
			1,
			"",
			cpuset.New(2),
		},
		{
			"take a socket of cpus from dual socket with HT",
			topoDualSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11),
			6,
			"",
			cpuset.New(0, 2, 4, 6, 8, 10),
		},
	}

	for _, tc := range testCases {
		result, err := takeByTopologyNUMAPacked(tc.topo, tc.availableCPUs, tc.numCPUs)
		if tc.expErr != "" && err.Error() != tc.expErr {
			t.Errorf("expected error to be [%v] but it was [%v] in test \"%s\"", tc.expErr, err, tc.description)
		}
		if !result.Equals(tc.expResult) {
			t.Errorf("expected result [%s] to equal [%s] in test \"%s\"", result, tc.expResult, tc.description)
		}
	}
}

func TestCPUAccumulatorFreeUncoreCache(t *testing.T) {
	testCases := []struct {
		description   string
		topo          *topology.CPUTopology
		availableCPUs cpuset.CPUSet
		expect        []int
	}{
		{
			"dual UncoreCache groups, 1 uncore cache free, cache id and cpu numbers (0:7, 1:8)",
			topoDualUncoreCacheSingleSocketHT,
			cpuset.New(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15),
			[]int{1},
		},
		{
			"dual UncoreCache groups, 2 uncore cache free, cache id and cpu numbers (0:8, 1:8)",
			topoDualUncoreCacheSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15),
			[]int{0, 1},
		},
		{
			"dual UncoreCache groups, 1 uncore cache free, cache id and cpu numbers (0:8, 1:7)",
			topoDualUncoreCacheSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14),
			[]int{0},
		},
		{
			"dual UncoreCache groups, 0 uncore cache free, cache id and cpu numbers (0:7, 1:7)",
			topoDualUncoreCacheSingleSocketHT,
			cpuset.New(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14),
			[]int{},
		},
	}
	for _, tc := range testCases {
		acc := newCPUAccumulator(tc.topo, tc.availableCPUs, 0)
		result := acc.freeUncoreCaches()
		if !reflect.DeepEqual(result, tc.expect) {
			t.Errorf("[%s] expected %v to equal %v", tc.description, result, tc.expect)
		}
	}
}

func TestTakeByTopologyUncoreCacheEnabledLegacy(t *testing.T) {
	testCases := []struct {
		description   string
		topo          *topology.CPUTopology
		availableCPUs cpuset.CPUSet
		numCPUs       int
		expErr        string
		expResult     cpuset.CPUSet
	}{
		// None of the topologies in this test should have more than one uncore cache
		// e.g. the old tests should not change
		{
			"take more cpus than are available from single socket with HT",
			topoSingleSocketHT,
			cpuset.New(0, 2, 4, 6),
			5,
			"not enough cpus available to satisfy request: requested=5, available=4",
			cpuset.New(),
		},
		{
			"take zero cpus from single socket with HT",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7),
			0,
			"",
			cpuset.New(),
		},
		{
			"take one cpu from single socket with HT",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7),
			1,
			"",
			cpuset.New(0),
		},
		{
			"take one cpu from single socket with HT, some cpus are taken",
			topoSingleSocketHT,
			cpuset.New(1, 3, 5, 6, 7),
			1,
			"",
			cpuset.New(6),
		},
		{
			"take two cpus from single socket with HT",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7),
			2,
			"",
			cpuset.New(0, 4),
		},
		{
			"take all cpus from single socket with HT",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7),
			8,
			"",
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7),
		},
		{
			"take two cpus from single socket with HT, only one core totally free",
			topoSingleSocketHT,
			cpuset.New(0, 1, 2, 3, 6),
			2,
			"",
			cpuset.New(2, 6),
		},
		{
			"take one cpu from dual socket with HT - core from Socket 0",
			topoDualSocketHT,
			cpuset.New(1, 2, 3, 4, 5, 7, 8, 9, 10, 11),
			1,
			"",
			cpuset.New(2),
		},
		{
			"take a socket of cpus from dual socket with HT",
			topoDualSocketHT,
			cpuset.New(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11),
			6,
			"",
			cpuset.New(0, 2, 4, 6, 8, 10),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			result, err := takeByTopologyNUMAPacked(tc.topo, tc.availableCPUs, tc.numCPUs)
			if tc.expErr != "" && err.Error() != tc.expErr {
				t.Errorf("[%s] expected %v to equal %v", tc.description, err, tc.expErr)
			}
			if !result.Equals(tc.expResult) {
				t.Errorf("[%s] expected %v to equal %v", tc.description, result, tc.expResult)
			}
		})
	}
}

func TestTakeIterative(t *testing.T) {
	examples := []struct {
		name        string
		topo        *topology.CPUTopology
		takeNumCpus []int
		expected    string
	}{
		{
			name: "epyc 7502p",
			// This topology has eight UCCs on one NUMA (8x8 UCCs)
			topo: AMD_EPYC_7502P_32_Core_Processor,
			// Take almost all the cpus per ucc per iteration (a degenerate, but interesting case)
			takeNumCpus: []int{7, 7, 7, 7, 7, 7, 7, 7},
			expected:    "[idle cpus=0-63] [step=0 count=7 cpus=0-3,32-34] [step=1 count=7 cpus=4-7,36-38]",
		},
		{
			name: "xeon gold 5120",
			// This topology has two UCCs with matching NUMA (2x28 UCCs)
			topo: Intel_R__Xeon_R__Gold_5120_CPU___2_20GHz,
			// Take an increasing number of cpus (in each iteration) to see the assignments
			takeNumCpus: []int{4, 5, 6, 7, 8}, // this combination exercises the lower level "takers", leaves holes, etc.
			// With the feature flag enabled the enhanced scheduler takes only from UCC1 to satisfy the entire request
			expected: "map[1:0,2,28,30 2:4,6,8,32,34 3:10,12,14,38,40,42 4:16,18,20,36,44,46,48 5:1,3,5,7,29,31,33,35]",
		},
		{
			name: "xeon gold 5218 - 1",
			// Also has two uncore caches
			topo:        gold_5218_topology,
			takeNumCpus: []int{1, 2, 3, 4, 5, 6},
			expected:    "map[1:0 2:4-5 3:8-9,12 4:10-11,14-15 5:2-3,6-7,16 6:20-21,24-25,28-29]",
		},
		{
			name:        "xeon gold 5218 - 2",
			topo:        gold_5218_topology,
			takeNumCpus: []int{4, 5, 6, 7},
			expected:    "map[1:0-1,4-5 2:8-9,12-14 3:2-3,6-7,10-11 4:16-17,20-21,24-25,28]",
		},
	}

	for _, tc := range examples {
		t.Run(tc.name, func(t *testing.T) {
			got := takeIterator(tc.topo, tc.takeNumCpus)
			if got != tc.expected {
				t.Errorf("\nEXPECTED: %v\nTO EQUAL: %v", tc.expected, got)
			}
		})
	}
}

func takeIterator(topo *topology.CPUTopology, takeNumCpus []int) string {
	var results []string
	cpuSet := topo.CPUDetails.CPUs()

	allCpus := cpuSet.Clone()

	for idx, takeCpus := range takeNumCpus {
		took, err := takeByTopologyNUMAPacked(topo, cpuSet, takeCpus)
		if err != nil {
			return fmt.Sprint(err)
		}
		results = append(results, fmt.Sprintf("[step=%d count=%d cpus=%s]", idx, takeCpus, took.String()))
		cpuSet = cpuSet.Difference(took)
	}
	return fmt.Sprintf("[idle cpus=%s] ", allCpus.String()) + strings.Join(results, " ")
}
