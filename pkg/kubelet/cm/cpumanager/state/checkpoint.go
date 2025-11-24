/*
Copyright 2018 The Kubernetes Authors.

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

package state

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"

	"k8s.io/apimachinery/pkg/util/dump"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/checksum"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/errors"
	"k8s.io/utils/cpuset"
)

var _ checkpointmanager.Checkpoint = &CPUManagerCheckpointV2{}
var _ checkpointmanager.Checkpoint = &CPUManagerCheckpointV3{}
var _ checkpointmanager.Checkpoint = &CPUManagerCheckpoint{}

type CPUManagerGlobalState struct {
	CPUSet string `json:"cpuset"`
}

type CPUManagerPodState struct {
	CPUSet string `json:"cpuset"`
}

type CPUManagerContainerState struct {
	CPUSet string `json:"cpuset"`
}

// CPUManagerCheckpoint struct is used to store cpu/pod assignments in a checkpoint in v2 format
type CPUManagerCheckpointV2 struct {
	PolicyName    string                       `json:"policyName"`
	DefaultCPUSet string                       `json:"defaultCpuSet"`
	Entries       map[string]map[string]string `json:"entries,omitempty"`
	Checksum      checksum.Checksum            `json:"checksum"`
}

// CPUManagerCheckpointV3 struct is used to store cpu/pod assignments in a checkpoint in v3 format
type CPUManagerCheckpointV3 struct {
	PolicyName       string                                         `json:"policyName"`
	Global           CPUManagerGlobalState                          `json:"globalEntries,omitempty"`
	PodEntries       map[string]CPUManagerPodState                  `json:"podEntries,omitempty"`
	ContainerEntries map[string]map[string]CPUManagerContainerState `json:"containerEntries,omitempty"`
	// V2 compatibility
	DefaultCPUSet string                       `json:"defaultCpuSet"`
	Entries       map[string]map[string]string `json:"entries,omitempty"`
	Checksum      checksum.Checksum            `json:"checksum"`
}

// CPUManagerCheckpointV2 struct is used to store cpu/pod assignments in a checkpoint in v2 format
type CPUManagerCheckpoint = CPUManagerCheckpointV3

// NewCPUManagerCheckpoint returns an instance of Checkpoint
func NewCPUManagerCheckpoint() *CPUManagerCheckpoint {
	//nolint:staticcheck // unexported-type-in-api user-facing error message
	return newCPUManagerCheckpointV3()
}

func newCPUManagerCheckpointV2() *CPUManagerCheckpointV2 {
	return &CPUManagerCheckpointV2{
		Entries: make(map[string]map[string]string),
	}
}

func newCPUManagerCheckpointV3() *CPUManagerCheckpointV3 {
	return &CPUManagerCheckpointV3{
		PodEntries:       make(map[string]CPUManagerPodState),
		ContainerEntries: make(map[string]map[string]CPUManagerContainerState),
	}
}

func (cp *CPUManagerCheckpointV3) clearV2CompatFields() {
	cp.DefaultCPUSet = ""
	cp.Entries = nil
}

func (cp *CPUManagerCheckpointV3) computeV2CompatFields() {
	cp.DefaultCPUSet = cp.Global.CPUSet
	cp.Entries = make(map[string]map[string]string)
	for podUID, containers := range cp.ContainerEntries {
		cp.Entries[podUID] = make(map[string]string)
		for containerName, state := range containers {
			cp.Entries[podUID][containerName] = state.CPUSet
		}
	}
}

func (cp *CPUManagerCheckpointV3) updateFromCPUAssignment(assignments ContainerCPUAssignments) {
	for pod := range assignments {
		cp.ContainerEntries[pod] = make(map[string]CPUManagerContainerState, len(assignments[pod]))
		for container, cset := range assignments[pod] {
			cp.ContainerEntries[pod][container] = CPUManagerContainerState{
				CPUSet: cset.String(),
			}
		}
	}
}

func (cp *CPUManagerCheckpointV3) computeCPUAssignments() (ContainerCPUAssignments, error) {
	tmpAssignments := ContainerCPUAssignments{}
	for pod := range cp.ContainerEntries {
		tmpAssignments[pod] = make(map[string]cpuset.CPUSet, len(cp.ContainerEntries[pod]))
		for containerName, cntState := range cp.ContainerEntries[pod] {
			tmpContainerCPUSet, err := cpuset.Parse(cntState.CPUSet)
			if err != nil {
				return nil, fmt.Errorf("could not parse cpuset %q for container %q in pod %q: %v", cntState.CPUSet, containerName, pod, err)
			}
			tmpAssignments[pod][containerName] = tmpContainerCPUSet
		}
	}
	return tmpAssignments, nil
}

// MarshalCheckpoint returns marshalled checkpoint in v2 format
func (cp *CPUManagerCheckpointV2) MarshalCheckpoint() ([]byte, error) {
	// make sure checksum wasn't set before so it doesn't affect output checksum
	cp.Checksum = 0
	cp.Checksum = checksum.New(cp)
	return json.Marshal(*cp)
}

// MarshalCheckpoint returns marshalled checkpoint in v3 format
func (cp *CPUManagerCheckpointV3) MarshalCheckpoint() ([]byte, error) {
	// make sure checksum wasn't set before so it doesn't affect output checksum
	cp.Checksum = 0
	cp.Checksum = checksum.New(cp)
	return json.Marshal(*cp)
}

// UnmarshalCheckpoint tries to unmarshal passed bytes to checkpoint in v2 format
func (cp *CPUManagerCheckpointV2) UnmarshalCheckpoint(blob []byte) error {
	return json.Unmarshal(blob, cp)
}

// UnmarshalCheckpoint tries to unmarshal passed bytes to checkpoint in v2 format
func (cp *CPUManagerCheckpointV3) UnmarshalCheckpoint(blob []byte) error {
	return json.Unmarshal(blob, cp)
}

// VerifyChecksum verifies that current checksum of checkpoint is valid in v2 format
func (cp *CPUManagerCheckpointV2) VerifyChecksum() error {
	if cp.Checksum == 0 {
		// accept empty checksum for compatibility with old file backend
		return nil
	}
	ck := cp.Checksum
	cp.Checksum = 0
	object := dump.ForHash(cp)
	object = strings.Replace(object, "CPUManagerCheckpointV2", "CPUManagerCheckpoint", 1)
	cp.Checksum = ck

	hash := fnv.New32a()
	fmt.Fprintf(hash, "%v", object)
	actualCS := checksum.Checksum(hash.Sum32())
	if cp.Checksum != actualCS {
		return &errors.CorruptCheckpointError{
			ActualCS:   uint64(actualCS),
			ExpectedCS: uint64(cp.Checksum),
		}
	}

	return nil
}

// VerifyChecksum verifies that current checksum of checkpoint is valid in v2 format
func (cp *CPUManagerCheckpointV3) VerifyChecksum() error {
	ck := cp.Checksum
	cp.Checksum = 0
	err := ck.Verify(cp)
	cp.Checksum = ck
	return err
}
