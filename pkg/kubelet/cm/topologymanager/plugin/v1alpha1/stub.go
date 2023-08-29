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
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	watcherapi "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
	pluginapi "k8s.io/kubelet/pkg/apis/topologymanager/v1alpha1"
)

// Stub implementation for PolicyPlugin.
type Stub struct {
	name   string
	socket string

	stop chan interface{}
	wg   sync.WaitGroup

	server *grpc.Server

	// mergeFunc is used for handling allocation request
	mergeFunc stubMergeFunc

	registrationStatus chan watcherapi.RegistrationStatus // for testing
	endpoint           string                             // for testing

}

type stubMergeFunc func(ctx context.Context, r *pluginapi.MergeHintsRequest) (*pluginapi.MergeHintsResponse, error)

func defaultMergeFunc(ctx context.Context, r *pluginapi.MergeHintsRequest) (*pluginapi.MergeHintsResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// NewPolicyPluginStub returns an initialized PolicyPlugin Stub.
func NewPolicyPluginStub(socket string, name string) *Stub {
	return &Stub{
		name:      name,
		socket:    socket,
		stop:      make(chan interface{}),
		mergeFunc: defaultMergeFunc,
	}
}

// SetMergeFunc sets mergeFunc of the device plugin
func (m *Stub) SetMergeFunc(f stubMergeFunc) *Stub {
	m.mergeFunc = f
	return m
}

// Start starts the gRPC server of the device plugin. Can only
// be called once.
func (m *Stub) Start() error {
	err := m.cleanup()
	if err != nil {
		return err
	}

	sock, err := net.Listen("unix", m.socket)
	if err != nil {
		return err
	}

	m.wg.Add(1)
	m.server = grpc.NewServer([]grpc.ServerOption{}...)
	pluginapi.RegisterPolicyPluginServer(m.server, m)
	watcherapi.RegisterRegistrationServer(m.server, m)

	go func() {
		defer m.wg.Done()
		m.server.Serve(sock)
	}()

	var lastDialErr error
	wait.PollImmediate(1*time.Second, 10*time.Second, func() (bool, error) {
		var conn *grpc.ClientConn
		_, conn, lastDialErr = dial(m.socket)
		if lastDialErr != nil {
			return false, nil
		}
		conn.Close()
		return true, nil
	})
	if lastDialErr != nil {
		return lastDialErr
	}

	klog.InfoS("Starting to serve on socket", "socket", m.socket)
	return nil
}

// Stop stops the gRPC server. Can be called without a prior Start
// and more than once. Not safe to be called concurrently by different
// goroutines!
func (m *Stub) Stop() error {
	if m.server == nil {
		return nil
	}
	m.server.Stop()
	m.wg.Wait()
	m.server = nil
	close(m.stop) // This prevents re-starting the server.

	return m.cleanup()
}

// GetInfo is the RPC which return pluginInfo
func (m *Stub) GetInfo(ctx context.Context, req *watcherapi.InfoRequest) (*watcherapi.PluginInfo, error) {
	klog.InfoS("GetInfo")
	return &watcherapi.PluginInfo{
		Type:              watcherapi.PolicyPlugin,
		Name:              m.name,
		Endpoint:          m.endpoint,
		SupportedVersions: []string{pluginapi.Version}}, nil
}

// NotifyRegistrationStatus receives the registration notification from watcher
func (m *Stub) NotifyRegistrationStatus(ctx context.Context, status *watcherapi.RegistrationStatus) (*watcherapi.RegistrationStatusResponse, error) {
	if m.registrationStatus != nil {
		m.registrationStatus <- *status
	}
	if !status.PluginRegistered {
		klog.InfoS("Registration failed", "err", status.Error)
	}
	return &watcherapi.RegistrationStatusResponse{}, nil
}

// Register registers the policy plugin to the Kubelet.
func (m *Stub) Register(kubeletEndpoint, name string, pluginSockDir string) error {
	if pluginSockDir != "" {
		if _, err := os.Stat(pluginSockDir + "DEPRECATION"); err == nil {
			klog.InfoS("Deprecation file found. Skip registration")
			return nil
		}
	}
	klog.InfoS("Deprecation file not found. Invoke registration")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, kubeletEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", addr)
		}))
	if err != nil {
		return err
	}
	defer conn.Close()
	client := pluginapi.NewRegistrationClient(conn)
	reqt := &pluginapi.RegisterRequest{
		Version:  pluginapi.Version,
		Endpoint: filepath.Base(m.socket),
		Name:     name,
	}

	_, err = client.Register(context.Background(), reqt)
	return err
}

func (m *Stub) MergeHints(ctx context.Context, r *pluginapi.MergeHintsRequest) (*pluginapi.MergeHintsResponse, error) {
	klog.InfoS("MergeHints", "request", r)
	return m.mergeFunc(ctx, r)
}

func (m *Stub) cleanup() error {
	if err := os.Remove(m.socket); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
