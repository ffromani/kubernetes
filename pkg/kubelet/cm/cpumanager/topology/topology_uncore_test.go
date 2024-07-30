/*
Copyright 2017 The Kubernetes Authors.

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

package topology

import (
	"testing"

	cadvisorapi "github.com/google/cadvisor/info/v1"
	"github.com/google/go-cmp/cmp"
	"k8s.io/utils/cpuset"
)

func TestDiscoverUncore(t *testing.T) {
	tests := []struct {
		name        string
		machineInfo cadvisorapi.MachineInfo
		want        *CPUTopology
		wantErr     bool
	}{
		{
			name: "OneSocketHT",
			machineInfo: cadvisorapi.MachineInfo{
				NumCores:   8,
				NumSockets: 1,
				Topology: []cadvisorapi.Node{
					{Id: 0,
						Cores: []cadvisorapi.Core{
							{SocketID: 0, Id: 0, Threads: []int{0, 4}},
							{SocketID: 0, Id: 1, Threads: []int{1, 5}},
							{SocketID: 0, Id: 2, Threads: []int{2, 6}},
							{SocketID: 0, Id: 3, Threads: []int{3, 7}},
						},
					},
				},
			},
			want: &CPUTopology{
				NumCPUs:      8,
				NumNUMANodes: 1,
				NumSockets:   1,
				NumCores:     4,
				CPUDetails: map[int]CPUInfo{
					0: {CoreID: 0, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					1: {CoreID: 1, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					2: {CoreID: 2, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					3: {CoreID: 3, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					4: {CoreID: 0, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					5: {CoreID: 1, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					6: {CoreID: 2, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					7: {CoreID: 3, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
				},
			},
			wantErr: false,
		},
		{
			name: "Dual uncore caches HT",
			machineInfo: cadvisorapi.MachineInfo{
				NumCores:   8,
				NumSockets: 1,
				Topology: []cadvisorapi.Node{
					{Id: 0,
						Cores: []cadvisorapi.Core{
							{Id: 0,
								Threads: []int{0, 4},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 0, Level: 3, Type: "unified", Size: 16 * 1024 * 1024},
								},
							},
							{Id: 1,
								Threads: []int{1, 5},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 0, Level: 3, Type: "unified", Size: 16 * 1024 * 1024},
								},
							},
							{Id: 2,
								Threads: []int{2, 6},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 1, Level: 3, Type: "unified", Size: 16 * 1024 * 1024},
								},
							},
							{Id: 3,
								Threads: []int{3, 7},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 1, Level: 3, Type: "unified", Size: 16 * 1024 * 1024},
								},
							},
						},
					},
				},
			},
			want: &CPUTopology{
				NumCPUs:         8,
				NumNUMANodes:    1,
				NumSockets:      1,
				NumCores:        4,
				NumUnCoreCaches: 2,
				CPUDetails: map[int]CPUInfo{
					0: {CoreID: 0, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
					1: {CoreID: 1, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
					2: {CoreID: 2, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 1},
					3: {CoreID: 3, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 1},
					4: {CoreID: 0, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
					5: {CoreID: 1, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 0},
					6: {CoreID: 2, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 1},
					7: {CoreID: 3, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: 1},
				},
			},
			wantErr: true,
		},
		{
			name: "DualSocketNoHT",
			machineInfo: cadvisorapi.MachineInfo{
				NumCores:   4,
				NumSockets: 2,
				Topology: []cadvisorapi.Node{
					{Id: 0,
						Cores: []cadvisorapi.Core{
							{SocketID: 0, Id: 0, Threads: []int{0}},
							{SocketID: 0, Id: 2, Threads: []int{2}},
						},
					},
					{Id: 1,
						Cores: []cadvisorapi.Core{
							{SocketID: 1, Id: 1, Threads: []int{1}},
							{SocketID: 1, Id: 3, Threads: []int{3}},
						},
					},
				},
			},
			want: &CPUTopology{
				NumCPUs:      4,
				NumNUMANodes: 2,
				NumSockets:   2,
				NumCores:     4,
				CPUDetails: map[int]CPUInfo{
					0: {CoreID: 0, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					1: {CoreID: 1, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: -1},
					2: {CoreID: 2, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					3: {CoreID: 3, SocketID: 1, NUMANodeID: 1, UnCoreCacheID: -1},
				},
			},
			wantErr: false,
		},
		{
			name: "SingleSocketMultiCache",
			machineInfo: cadvisorapi.MachineInfo{
				NumCores:         32,
				NumPhysicalCores: 16,
				NumSockets:       1,
				Topology: []cadvisorapi.Node{
					{Id: 0,
						Cores: []cadvisorapi.Core{
							{
								Id:       0,
								Threads:  []int{0, 16},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 0, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 0, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 0, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 0, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       1,
								Threads:  []int{1, 17},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 1, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 1, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 1, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 0, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       2,
								Threads:  []int{2, 18},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 2, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 2, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 2, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 0, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       3,
								Threads:  []int{3, 19},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 3, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 3, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 3, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 0, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       4,
								Threads:  []int{4, 20},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 4, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 4, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 4, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 0, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       5,
								Threads:  []int{5, 21},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 5, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 5, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 5, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 0, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       6,
								Threads:  []int{6, 22},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 6, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 6, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 6, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 0, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       7,
								Threads:  []int{7, 23},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 7, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 7, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 7, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 0, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       8,
								Threads:  []int{8, 24},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 8, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 8, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 8, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 1, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       9,
								Threads:  []int{9, 25},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 9, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 9, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 9, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 1, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       10,
								Threads:  []int{10, 26},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 10, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 10, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 10, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 1, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       11,
								Threads:  []int{11, 27},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 11, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 11, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 11, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 1, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       12,
								Threads:  []int{12, 28},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 12, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 12, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 12, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 1, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       13,
								Threads:  []int{13, 29},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 13, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 13, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 13, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 1, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       14,
								Threads:  []int{14, 30},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 14, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 14, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 14, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 1, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
							{
								Id:       15,
								Threads:  []int{15, 31},
								SocketID: 0,
								Caches: []cadvisorapi.Cache{
									{Id: 15, Level: 1, Type: "Data", Size: 32 * 1024},
									{Id: 15, Level: 1, Type: "Instruction", Size: 32 * 1024},
									{Id: 15, Level: 2, Type: "Unified", Size: 512 * 1024},
								},
								UncoreCaches: []cadvisorapi.Cache{
									{Id: 1, Level: 3, Type: "Unified", Size: 32 * 1024 * 1024},
								},
							},
						},
					},
				},
			},
			want: &CPUTopology{
				NumCPUs:         32,
				NumNUMANodes:    1,
				NumSockets:      1,
				NumCores:        16,
				NumUnCoreCaches: 2,
				CPUDetails: map[int]CPUInfo{
					0:  {CoreID: 0, UnCoreCacheID: 0},
					1:  {CoreID: 1, UnCoreCacheID: 0},
					2:  {CoreID: 2, UnCoreCacheID: 0},
					3:  {CoreID: 3, UnCoreCacheID: 0},
					4:  {CoreID: 4, UnCoreCacheID: 0},
					5:  {CoreID: 5, UnCoreCacheID: 0},
					6:  {CoreID: 6, UnCoreCacheID: 0},
					7:  {CoreID: 7, UnCoreCacheID: 0},
					8:  {CoreID: 8, UnCoreCacheID: 1},
					9:  {CoreID: 9, UnCoreCacheID: 1},
					10: {CoreID: 10, UnCoreCacheID: 1},
					11: {CoreID: 11, UnCoreCacheID: 1},
					12: {CoreID: 12, UnCoreCacheID: 1},
					13: {CoreID: 13, UnCoreCacheID: 1},
					14: {CoreID: 14, UnCoreCacheID: 1},
					15: {CoreID: 15, UnCoreCacheID: 1},
					16: {CoreID: 0, UnCoreCacheID: 0},
					17: {CoreID: 1, UnCoreCacheID: 0},
					18: {CoreID: 2, UnCoreCacheID: 0},
					19: {CoreID: 3, UnCoreCacheID: 0},
					20: {CoreID: 4, UnCoreCacheID: 0},
					21: {CoreID: 5, UnCoreCacheID: 0},
					22: {CoreID: 6, UnCoreCacheID: 0},
					23: {CoreID: 7, UnCoreCacheID: 0},
					24: {CoreID: 8, UnCoreCacheID: 1},
					25: {CoreID: 9, UnCoreCacheID: 1},
					26: {CoreID: 10, UnCoreCacheID: 1},
					27: {CoreID: 11, UnCoreCacheID: 1},
					28: {CoreID: 12, UnCoreCacheID: 1},
					29: {CoreID: 13, UnCoreCacheID: 1},
					30: {CoreID: 14, UnCoreCacheID: 1},
					31: {CoreID: 15, UnCoreCacheID: 1},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Discover(&tt.machineInfo)
			if err != nil {
				if tt.wantErr {
					t.Logf("Discover() expected error = %v", err)
				} else {
					t.Errorf("Discover() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}
			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Errorf("DiscoverUncore() = %v, want %v diff=%s", got, tt.want, diff)
			}
		})
	}
}

func TestUncoreCachesFuncs(t *testing.T) {
	tests := []struct {
		name           string
		cpuTopo        *CPUTopology
		socketIDs      []int
		cpusInSocket   cpuset.CPUSet
		numaNodeIDs    []int
		cpusInNUMANode cpuset.CPUSet
	}{
		{
			name: "OneSocketHT",
			cpuTopo: &CPUTopology{
				NumCPUs:      8,
				NumNUMANodes: 1,
				NumSockets:   1,
				NumCores:     4,
				CPUDetails: map[int]CPUInfo{
					0: {CoreID: 0, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					1: {CoreID: 1, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					2: {CoreID: 2, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					3: {CoreID: 3, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					4: {CoreID: 0, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					5: {CoreID: 1, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					6: {CoreID: 2, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
					7: {CoreID: 3, SocketID: 0, NUMANodeID: 0, UnCoreCacheID: -1},
				},
			},
			socketIDs:      []int{0},
			cpusInSocket:   cpuset.New(-1),
			numaNodeIDs:    []int{0},
			cpusInNUMANode: cpuset.New(-1),
		},
		{
			name: "SingleSocketMultiCache",
			cpuTopo: &CPUTopology{
				NumCPUs:         32,
				NumNUMANodes:    1,
				NumSockets:      1,
				NumCores:        16,
				NumUnCoreCaches: 2,
				CPUDetails: map[int]CPUInfo{
					0:  {CoreID: 0, UnCoreCacheID: 0},
					1:  {CoreID: 1, UnCoreCacheID: 0},
					2:  {CoreID: 2, UnCoreCacheID: 0},
					3:  {CoreID: 3, UnCoreCacheID: 0},
					4:  {CoreID: 4, UnCoreCacheID: 0},
					5:  {CoreID: 5, UnCoreCacheID: 0},
					6:  {CoreID: 6, UnCoreCacheID: 0},
					7:  {CoreID: 7, UnCoreCacheID: 0},
					8:  {CoreID: 8, UnCoreCacheID: 1},
					9:  {CoreID: 9, UnCoreCacheID: 1},
					10: {CoreID: 10, UnCoreCacheID: 1},
					11: {CoreID: 11, UnCoreCacheID: 1},
					12: {CoreID: 12, UnCoreCacheID: 1},
					13: {CoreID: 13, UnCoreCacheID: 1},
					14: {CoreID: 14, UnCoreCacheID: 1},
					15: {CoreID: 15, UnCoreCacheID: 1},
					16: {CoreID: 0, UnCoreCacheID: 0},
					17: {CoreID: 1, UnCoreCacheID: 0},
					18: {CoreID: 2, UnCoreCacheID: 0},
					19: {CoreID: 3, UnCoreCacheID: 0},
					20: {CoreID: 4, UnCoreCacheID: 0},
					21: {CoreID: 5, UnCoreCacheID: 0},
					22: {CoreID: 6, UnCoreCacheID: 0},
					23: {CoreID: 7, UnCoreCacheID: 0},
					24: {CoreID: 8, UnCoreCacheID: 1},
					25: {CoreID: 9, UnCoreCacheID: 1},
					26: {CoreID: 10, UnCoreCacheID: 1},
					27: {CoreID: 11, UnCoreCacheID: 1},
					28: {CoreID: 12, UnCoreCacheID: 1},
					29: {CoreID: 13, UnCoreCacheID: 1},
					30: {CoreID: 14, UnCoreCacheID: 1},
					31: {CoreID: 15, UnCoreCacheID: 1},
				},
			},
			socketIDs:      []int{0},
			cpusInSocket:   cpuset.New(0, 1),
			numaNodeIDs:    []int{0},
			cpusInNUMANode: cpuset.New(0, 1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSock := tt.cpuTopo.CPUDetails.UncoreCachesInSocket(tt.socketIDs...)
			if gotSock.String() != tt.cpusInSocket.String() {
				t.Errorf("cpusInSocket %q, want %q", gotSock.String(), tt.cpusInSocket.String())
			}

			gotNuma := tt.cpuTopo.CPUDetails.UncoreCachesInNUMANode(tt.numaNodeIDs...)
			if gotNuma.String() != tt.cpusInNUMANode.String() {
				t.Errorf("cpusInSocket %q, want %q", gotNuma.String(), tt.cpusInNUMANode.String())
			}
		})
	}
}
