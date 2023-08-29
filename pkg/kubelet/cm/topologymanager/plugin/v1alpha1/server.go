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
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/opencontainers/selinux/go-selinux"
	"google.golang.org/grpc"

	"k8s.io/klog/v2"
	api "k8s.io/kubelet/pkg/apis/topologymanager/v1alpha1"
	"k8s.io/kubernetes/pkg/kubelet/config"
	"k8s.io/kubernetes/pkg/kubelet/pluginmanager/cache"
)

// Server interface provides methods for Device plugin registration server.
type Server interface {
	cache.PluginHandler
	Start() error
	Stop() error
	SocketPath() string
}

type server struct {
	socketName string
	socketDir  string
	mutex      sync.Mutex
	wg         sync.WaitGroup
	grpc       *grpc.Server
	rhandler   RegistrationHandler
	chandler   ClientHandler
	clients    map[string]Client
}

// NewServer returns an initialized policy plugin registration server.
func NewServer(socketPath string, rh RegistrationHandler, ch ClientHandler) (Server, error) {
	if socketPath == "" || !filepath.IsAbs(socketPath) {
		return nil, fmt.Errorf(errBadSocket+" %s", socketPath)
	}

	dir, name := filepath.Split(socketPath)

	klog.V(2).InfoS("Creating policy plugin registration server", "version", api.Version, "socket", socketPath)
	s := &server{
		socketName: name,
		socketDir:  dir,
		rhandler:   rh,
		chandler:   ch,
		clients:    make(map[string]Client),
	}

	return s, nil
}

func (s *server) Start() error {
	klog.V(2).InfoS("Starting policy plugin registration server")

	if err := os.MkdirAll(s.socketDir, 0750); err != nil {
		klog.ErrorS(err, "Failed to create the policy plugin socket directory", "directory", s.socketDir)
		return err
	}

	if selinux.GetEnabled() {
		if err := selinux.SetFileLabel(s.socketDir, config.KubeletPluginsDirSELinuxLabel); err != nil {
			klog.InfoS("Unprivileged containerized plugins might not work. Could not set selinux context on socket dir", "path", s.socketDir, "err", err)
		}
	}

	// For now we leave cleanup of the *entire* directory up to the Handler
	// (even though we should in theory be able to just wipe the whole directory)
	// because the Handler stores its checkpoint file (amongst others) in here.
	if err := s.rhandler.CleanupPluginDirectory(s.socketDir); err != nil {
		klog.ErrorS(err, "Failed to cleanup the policy plugin directory", "directory", s.socketDir)
		return err
	}

	ln, err := net.Listen("unix", s.SocketPath())
	if err != nil {
		klog.ErrorS(err, "Failed to listen to socket while starting policy plugin registry")
		return err
	}

	s.wg.Add(1)
	s.grpc = grpc.NewServer([]grpc.ServerOption{}...)

	api.RegisterRegistrationServer(s.grpc, s)
	go func() {
		defer s.wg.Done()
		s.grpc.Serve(ln)
	}()

	return nil
}

func (s *server) Stop() error {
	s.visitClients(func(r string, c Client) {
		if err := s.disconnectClient(r, c); err != nil {
			klog.InfoS("Error disconnecting policy plugin client", "resourceName", r, "err", err)
		}
	})

	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.grpc == nil {
		return nil
	}

	s.grpc.Stop()
	s.wg.Wait()
	s.grpc = nil

	return nil
}

func (s *server) SocketPath() string {
	return filepath.Join(s.socketDir, s.socketName)
}

func (s *server) Register(ctx context.Context, r *api.RegisterRequest) (*api.RegisterResponse, error) {
	klog.InfoS("Got registration request from policy plugin")

	if !s.isVersionCompatibleWithPlugin(r.Version) {
		err := fmt.Errorf(errUnsupportedVersion, r.Version, api.SupportedVersions)
		klog.InfoS("Bad registration request from policy plugin with resource", "name", r.Name, "err", err)
		return &api.RegisterResponse{}, err
	}

	if err := s.connectClient(r.Name, filepath.Join(s.socketDir, r.Endpoint)); err != nil {
		klog.InfoS("Error connecting to policy plugin client", "err", err)
		return &api.RegisterResponse{}, err
	}

	return &api.RegisterResponse{}, nil
}

func (s *server) isVersionCompatibleWithPlugin(versions ...string) bool {
	// TODO(vikasc): Currently this is fine as we only have a single supported version. When we do need to support
	// multiple versions in the future, we may need to extend this function to return a supported version.
	// E.g., say kubelet supports v1alpha1 and v1beta2, and we get v1alpha1 and v1alpha1 from a policy plugin,
	// this function should return v1alpha1
	for _, version := range versions {
		for _, supportedVersion := range api.SupportedVersions {
			if version == supportedVersion {
				return true
			}
		}
	}
	return false
}

func (s *server) visitClients(visit func(r string, c Client)) {
	s.mutex.Lock()
	for r, c := range s.clients {
		s.mutex.Unlock()
		visit(r, c)
		s.mutex.Lock()
	}
	s.mutex.Unlock()
}
