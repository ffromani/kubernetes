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

package topologymanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	errorsutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/topologymanager/v1alpha1"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/bitmask"
	plugin "k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/plugin/v1alpha1"
)

type endpoint interface {
	merge(podUID, containerName string, resourceProperties []corev1.ResourceProperty, providersHints []map[string][]TopologyHint) (map[string]TopologyHint, error)

	isStopped() bool
	getName() string
}

type endpointImpl struct {
	mutex    sync.Mutex
	name     string
	api      pluginapi.PolicyPluginClient
	stopTime time.Time
	client   plugin.Client // for testing only
}

// newEndpointImpl creates a new endpoint for the given resourceName.
// This is to be used during normal device plugin registration.
func newEndpointImpl(p plugin.PolicyPlugin) *endpointImpl {
	return &endpointImpl{
		api:  p.API(),
		name: p.Name(),
	}
}

// newStoppedEndpointImpl creates a new endpoint for the given resourceName with stopTime set.
// This is to be used during Kubelet restart, before the actual device plugin re-registers.
func newStoppedEndpointImpl(name string) *endpointImpl {
	return &endpointImpl{
		name:     name,
		stopTime: time.Now(),
	}
}

func (e *endpointImpl) getName() string {
	// no lock, because the field is set once at init time
	return e.name
}

func (e *endpointImpl) isStopped() bool {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return !e.stopTime.IsZero()
}

// allocate issues Allocate gRPC call to the device plugin.
func (e *endpointImpl) merge(podUID, containerName string, resourceProperties []corev1.ResourceProperty, providersHints []map[string][]TopologyHint) (map[string]TopologyHint, error) {
	if e.isStopped() {
		return nil, fmt.Errorf("endpoint %q stopped", e.getName())
	}
	ret, err := e.api.MergeHints(context.Background(), &pluginapi.MergeHintsRequest{
		PodUID:        podUID,
		ContainerName: containerName,
		Hints:         flattenHints(providersHints),
		Properties:    translateProperties(resourceProperties),
	})
	if err != nil {
		return nil, err
	}
	if err := validateMergedHints(ret); err != nil {
		return nil, err
	}
	return collectHints(ret.Hints)
}

func validateMergedHints(resp *pluginapi.MergeHintsResponse) error {
	haveUniversalHint := false
	res := sets.New[string]()
	for _, hint := range resp.Hints {
		if hint.Resource == pluginapi.UniversalResource {
			haveUniversalHint = true
			continue
		}
		resName := string(hint.Resource)
		if res.Has(resName) {
			return fmt.Errorf("inconsistent response, duplicate resource %q", resName)
		}
		res.Insert(resName)
	}
	if !haveUniversalHint {
		return fmt.Errorf("incomplete response, missing universal hint")
	}
	return nil
}

func collectHints(hints []*pluginapi.TopologyHint) (map[string]TopologyHint, error) {
	ret := make(map[string]TopologyHint)
	for _, hint := range hints {
		aff, err := bitmask.NewBitMask(unpackTopology(hint.Topology)...)
		if err != nil {
			return nil, err
		}
		ret[hint.Resource] = TopologyHint{
			NUMANodeAffinity: aff,
			Preferred:        hint.Preferred,
		}
	}
	return ret, nil
}

func flattenHints(providersHints []map[string][]TopologyHint) []*pluginapi.TopologyHint {
	retHints := []*pluginapi.TopologyHint{}
	for _, providerHint := range providersHints {
		for resourceName, resourceHints := range providerHint {
			for _, hint := range resourceHints {
				retHints = append(retHints, &pluginapi.TopologyHint{
					Resource:  translateResource(resourceName),
					Preferred: hint.Preferred,
					Topology:  packTopology(hint.NUMANodeAffinity.GetBits()),
				})
			}
		}
	}
	return retHints
}

func translateProperties(resourceProperties []corev1.ResourceProperty) []*pluginapi.ResourceProperty {
	retProps := []*pluginapi.ResourceProperty{}
	for _, prop := range resourceProperties {
		retProps = append(retProps, &pluginapi.ResourceProperty{
			Resource:           string(prop.Name),
			LocalityToleration: string(prop.LocalityToleration),
		})
	}
	return retProps
}

func unpackTopology(topology *pluginapi.TopologyInfo) []int {
	ret := []int{}
	for _, node := range topology.Nodes {
		ret = append(ret, int(node.ID))
	}
	return ret
}

func packTopology(bits []int) *pluginapi.TopologyInfo {
	info := pluginapi.TopologyInfo{
		Nodes: make([]*pluginapi.NUMANode, 0, len(bits)),
	}
	for _, bit := range bits {
		info.Nodes = append(info.Nodes, &pluginapi.NUMANode{
			ID: int64(bit),
		})
	}
	return nil
}

func translateResource(name string) string {
	if name == pluginapi.UniversalResource {
		return "" // internal identifier for universal resource
	}
	return name
}

func cleanupPluginDirectory(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		filePath := filepath.Join(dir, name)
		// TODO: Until the bug - https://github.com/golang/go/issues/33357 is fixed, os.stat wouldn't return the
		// right mode(socket) on windows. Hence deleting the file, without checking whether
		// its a socket, on windows.
		stat, err := os.Lstat(filePath)
		if err != nil {
			klog.ErrorS(err, "Failed to stat file", "path", filePath)
			continue
		}
		if stat.IsDir() {
			continue
		}
		err = os.RemoveAll(filePath)
		if err != nil {
			errs = append(errs, err)
			klog.ErrorS(err, "Failed to remove file", "path", filePath)
			continue
		}
	}
	return errorsutil.NewAggregate(errs)
}
