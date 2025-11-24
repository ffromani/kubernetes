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
	"fmt"
	"path/filepath"
	"sync"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/errors"
	"k8s.io/kubernetes/pkg/kubelet/cm/containermap"
	"k8s.io/utils/cpuset"
)

var _ State = &stateCheckpoint{}

type stateCheckpoint struct {
	mux               sync.RWMutex
	logger            logr.Logger
	policyName        string
	cache             State
	checkpointManager checkpointmanager.CheckpointManager
	checkpointName    string
	initialContainers containermap.ContainerMap
}

// NewCheckpointState creates new State for keeping track of cpu/pod assignment with checkpoint backend
func NewCheckpointState(logger logr.Logger, stateDir, checkpointName, policyName string, initialContainers containermap.ContainerMap) (State, error) {
	// we store a logger instance because the checkpointmanager code gets no context yet, so it's pointless to add on our outer layer
	// since we store a checkpoint, we can use the relatively expensive "WithName".
	logger = klog.LoggerWithName(logger, "CPUManager state checkpoint")
	checkpointManager, err := checkpointmanager.NewCheckpointManager(stateDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize checkpoint manager: %v", err)
	}
	stateCheckpoint := &stateCheckpoint{
		logger:            logger,
		cache:             NewMemoryState(logger),
		policyName:        policyName,
		checkpointManager: checkpointManager,
		checkpointName:    checkpointName,
		initialContainers: initialContainers,
	}

	if err := stateCheckpoint.restoreState(); err != nil {
		//nolint:staticcheck // ST1005 user-facing error message
		return nil, fmt.Errorf("could not restore state from checkpoint: %v, please drain this node and delete the CPU manager checkpoint file %q before restarting Kubelet",
			err, filepath.Join(stateDir, checkpointName))
	}

	return stateCheckpoint, nil
}

// migrateV2CheckpointToV3Checkpoint() converts checkpoints from the v2 format to the v3 format
func (sc *stateCheckpoint) migrateV2CheckpointToV3Checkpoint(src *CPUManagerCheckpointV2, dst *CPUManagerCheckpointV3) error {
	if src.PolicyName != "" {
		dst.PolicyName = src.PolicyName
	}
	if src.DefaultCPUSet != "" {
		dst.Global.CPUSet = src.DefaultCPUSet
	}
	if dst.PodEntries == nil {
		dst.PodEntries = make(map[string]CPUManagerPodState)
	}
	if dst.ContainerEntries == nil {
		dst.ContainerEntries = make(map[string]map[string]CPUManagerContainerState)
	}
	for podUID, podState := range src.Entries {
		if _, exists := dst.ContainerEntries[podUID]; !exists {
			dst.ContainerEntries[podUID] = make(map[string]CPUManagerContainerState)
		}
		for containerName, cset := range podState {
			dst.ContainerEntries[podUID][containerName] = CPUManagerContainerState{
				CPUSet: cset,
			}
		}
	}
	return nil
}

func (sc *stateCheckpoint) loadState() (*CPUManagerCheckpointV3, error) {
	// happy and hopefully much more frequent happy path first
	checkpointV3 := newCPUManagerCheckpointV3()
	err := sc.checkpointManager.GetCheckpoint(sc.checkpointName, checkpointV3)
	if err == nil {
		return checkpointV3, nil
	}

	// supported fallback next
	checkpointV2 := newCPUManagerCheckpointV2()
	err = sc.checkpointManager.GetCheckpoint(sc.checkpointName, checkpointV2)
	if err == nil {
		err = sc.migrateV2CheckpointToV3Checkpoint(checkpointV2, checkpointV3)
		if err != nil {
			return nil, fmt.Errorf("error migrating v2 checkpoint state to v3 checkpoint state: %w", err)
		}
		return checkpointV3, nil
	}

	// everything failed, give up
	return nil, err
}

// restores state from a checkpoint and creates it if it doesn't exist
func (sc *stateCheckpoint) restoreState() error {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	var err error

	checkpointV3, err := sc.loadState()
	if err != nil {
		if err == errors.ErrCheckpointNotFound {
			return sc.storeState()
		}
		return err
	}

	if sc.policyName != checkpointV3.PolicyName {
		return fmt.Errorf("configured policy %q differs from state checkpoint policy %q", sc.policyName, checkpointV3.PolicyName)
	}

	tmpDefaultCPUSet, err := cpuset.Parse(checkpointV3.Global.CPUSet)
	if err != nil {
		return fmt.Errorf("could not parse default cpu set %q: %w", checkpointV3.Global.CPUSet, err)
	}

	tmpAssignments, err := checkpointV3.computeCPUAssignments()
	if err != nil {
		return err
	}

	sc.cache.SetDefaultCPUSet(tmpDefaultCPUSet)
	sc.cache.SetCPUAssignments(tmpAssignments)

	sc.logger.V(2).Info("restored state from checkpoint", "defaultCpuSet", tmpDefaultCPUSet.String())

	return nil
}

// saves state to a checkpoint, caller is responsible for locking
func (sc *stateCheckpoint) storeState() error {
	checkpoint := NewCPUManagerCheckpoint()

	checkpoint.PolicyName = sc.policyName
	checkpoint.Global.CPUSet = sc.cache.GetDefaultCPUSet().String()
	checkpoint.updateFromCPUAssignment(sc.cache.GetCPUAssignments())
	checkpoint.computeV2CompatFields()

	err := sc.checkpointManager.CreateCheckpoint(sc.checkpointName, checkpoint)
	if err != nil {
		sc.logger.Error(err, "Failed to save checkpoint")
		return err
	}
	return nil
}

// GetCPUSet returns current CPU set
func (sc *stateCheckpoint) GetCPUSet(podUID string, containerName string) (cpuset.CPUSet, bool) {
	sc.mux.RLock()
	defer sc.mux.RUnlock()

	return sc.cache.GetCPUSet(podUID, containerName)
}

// GetDefaultCPUSet returns default CPU set
func (sc *stateCheckpoint) GetDefaultCPUSet() cpuset.CPUSet {
	sc.mux.RLock()
	defer sc.mux.RUnlock()

	return sc.cache.GetDefaultCPUSet()
}

// GetCPUSetOrDefault returns current CPU set, or default one if it wasn't changed
func (sc *stateCheckpoint) GetCPUSetOrDefault(podUID string, containerName string) cpuset.CPUSet {
	sc.mux.RLock()
	defer sc.mux.RUnlock()

	return sc.cache.GetCPUSetOrDefault(podUID, containerName)
}

// GetCPUAssignments returns current CPU to pod assignments
func (sc *stateCheckpoint) GetCPUAssignments() ContainerCPUAssignments {
	sc.mux.RLock()
	defer sc.mux.RUnlock()

	return sc.cache.GetCPUAssignments()
}

// SetCPUSet sets CPU set
func (sc *stateCheckpoint) SetCPUSet(podUID string, containerName string, cset cpuset.CPUSet) {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	sc.cache.SetCPUSet(podUID, containerName, cset)
	err := sc.storeState()
	if err != nil {
		sc.logger.Error(err, "Failed to store state to checkpoint", "podUID", podUID, "containerName", containerName)
	}
}

// SetDefaultCPUSet sets default CPU set
func (sc *stateCheckpoint) SetDefaultCPUSet(cset cpuset.CPUSet) {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	sc.cache.SetDefaultCPUSet(cset)
	err := sc.storeState()
	if err != nil {
		sc.logger.Error(err, "Failed to store state to checkpoint")
	}
}

// SetCPUAssignments sets CPU to pod assignments
func (sc *stateCheckpoint) SetCPUAssignments(a ContainerCPUAssignments) {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	sc.cache.SetCPUAssignments(a)
	err := sc.storeState()
	if err != nil {
		sc.logger.Error(err, "Failed to store state to checkpoint")
	}
}

// Delete deletes assignment for specified pod
func (sc *stateCheckpoint) Delete(podUID string, containerName string) {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	sc.cache.Delete(podUID, containerName)
	err := sc.storeState()
	if err != nil {
		sc.logger.Error(err, "Failed to store state to checkpoint", "podUID", podUID, "containerName", containerName)
	}
}

// ClearState clears the state and saves it in a checkpoint
func (sc *stateCheckpoint) ClearState() {
	sc.mux.Lock()
	defer sc.mux.Unlock()
	sc.cache.ClearState()
	err := sc.storeState()
	if err != nil {
		sc.logger.Error(err, "Failed to store state to checkpoint")
	}
}
