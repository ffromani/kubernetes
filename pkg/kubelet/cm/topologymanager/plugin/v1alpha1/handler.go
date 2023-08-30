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

package v1alpha1

import (
	"fmt"
	"os"

	"k8s.io/klog/v2"
	api "k8s.io/kubelet/pkg/apis/topologymanager/v1alpha1"
	"k8s.io/kubernetes/pkg/kubelet/pluginmanager/cache"
)

func (s *server) GetPluginHandler() cache.PluginHandler {
	if f, err := os.Create(s.socketDir + "DEPRECATION"); err != nil {
		klog.ErrorS(err, "Failed to create deprecation file at socket dir", "path", s.socketDir)
	} else {
		f.Close()
		klog.V(4).InfoS("Created deprecation file", "path", f.Name())
	}
	return s
}

func (s *server) RegisterPlugin(pluginName string, endpoint string, versions []string) error {
	klog.V(2).InfoS("Registering plugin at endpoint", "plugin", pluginName, "endpoint", endpoint)
	return s.connectClient(pluginName, endpoint)
}

func (s *server) DeRegisterPlugin(pluginName string) {
	klog.V(2).InfoS("Deregistering plugin", "plugin", pluginName)
	client := s.getClient(pluginName)
	if client != nil {
		s.disconnectClient(pluginName, client)
	}
}

func (s *server) ValidatePlugin(pluginName string, endpoint string, versions []string) error {
	klog.V(2).InfoS("Got plugin at endpoint with versions", "plugin", pluginName, "endpoint", endpoint, "versions", versions)

	if !s.isVersionCompatibleWithPlugin(versions...) {
		return fmt.Errorf("manager version, %s, is not among plugin supported versions %v", api.Version, versions)
	}

	return nil
}

func (s *server) connectClient(name string, socketPath string) error {
	c := NewPluginClient(name, socketPath, s.chandler)

	s.registerClient(name, c)
	if err := c.Connect(); err != nil {
		s.deregisterClient(name)
		klog.ErrorS(err, "Failed to connect to new client", "resource", name)
		return err
	}

	return nil
}

func (s *server) disconnectClient(name string, c Client) error {
	s.deregisterClient(name)
	return c.Disconnect()
}

func (s *server) registerClient(name string, c Client) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.clients[name] = c
	klog.V(2).InfoS("Registered client", "name", name)
}

func (s *server) deregisterClient(name string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	delete(s.clients, name)
	klog.V(2).InfoS("Deregistered client", "name", name)
}

func (s *server) getClient(name string) Client {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.clients[name]
}
