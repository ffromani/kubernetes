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
	"fmt"
	"os"
	"runtime"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/topologymanager/v1alpha1"
	plugin "k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/plugin/v1alpha1"
	"k8s.io/kubernetes/pkg/kubelet/pluginmanager/cache"
)

type pluginPolicy struct {
	server plugin.Server
	mutex  sync.Mutex
	ep     endpoint
}

var _ Policy = &pluginPolicy{}

// PolicyPlugin policy name.
const PolicyPlugin string = "plugin"

// NewPluginPolicy returns plugin policy.
func NewPluginPolicy(_ PolicyOptions) (Policy, error) {
	socketPath := pluginapi.KubeletSocket
	if runtime.GOOS == "windows" {
		socketPath = os.Getenv("SYSTEMDRIVE") + pluginapi.KubeletSocketWindows
	}

	pol := &pluginPolicy{
		ep: newStoppedEndpointImpl("none"),
	}

	server, err := plugin.NewServer(socketPath, pol, pol)
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin server: %v", err)
	}
	pol.server = server
	return pol, nil
}

func (p *pluginPolicy) Name() string {
	return PolicyPlugin
}

func (p *pluginPolicy) Merge(podUID, containerName string, resourceProperties []corev1.ResourceProperty, providersHints []map[string][]TopologyHint) (map[string]TopologyHint, bool) {
	p.mutex.Lock()
	endpoint := p.ep
	p.mutex.Unlock()

	bestHint, err := endpoint.merge(podUID, containerName, resourceProperties, providersHints)
	if err != nil {
		klog.V(2).ErrorS(err, "Merge failed", "plugin", endpoint.getName())
		return bestHint, false
	}
	return bestHint, true
}

func (pol *pluginPolicy) GetWatcherHandler() cache.PluginHandler {
	return pol.server
}

func (pol *pluginPolicy) CleanupPluginDirectory(dir string) error {
	return cleanupPluginDirectory(dir)
}

// PluginConnected is to connect a plugin to a new endpoint.
// This is done as part of device plugin registration.
func (pol *pluginPolicy) PluginConnected(name string, p plugin.PolicyPlugin) error {
	pol.mutex.Lock()
	defer pol.mutex.Unlock()
	pol.ep = newEndpointImpl(p)

	klog.V(2).InfoS("Policy plugin connected", "name", name)
	return nil
}

func (pol *pluginPolicy) PluginDisconnected(resourceName string) {
	pol.mutex.Lock()
	defer pol.mutex.Unlock()
	name := pol.ep.getName()
	pol.ep = newStoppedEndpointImpl(name)

	klog.V(2).InfoS("Policy plugin disconnected", "name", name)
}
