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
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"k8s.io/klog/v2"
	api "k8s.io/kubelet/pkg/apis/topologymanager/v1alpha1"
)

// PolicyPlugin interface provides methods for accessing Device Plugin names, API and unix socket.
type PolicyPlugin interface {
	API() api.PolicyPluginClient
	SocketPath() string
	Name() string
}

// Client interface provides methods for establishing/closing gRPC connection and running the policy plugin gRPC client.
type Client interface {
	Connect() error
	Disconnect() error
}

type client struct {
	mutex   sync.Mutex
	name    string
	socket  string
	grpc    *grpc.ClientConn
	handler ClientHandler
	client  api.PolicyPluginClient
}

// NewPluginClient returns an initialized policy plugin client.
func NewPluginClient(name string, socketPath string, handler ClientHandler) Client {
	return &client{
		name:    name,
		socket:  socketPath,
		handler: handler,
	}
}

// Connect is for establishing a gRPC connection between device manager and policy plugin.
func (c *client) Connect() error {
	client, conn, err := dial(c.socket)
	if err != nil {
		klog.ErrorS(err, "Unable to connect to policy plugin client with socket path", "path", c.socket)
		return err
	}
	c.grpc = conn
	c.client = client
	return c.handler.PluginConnected(c.name, c)
}

// Disconnect is for closing gRPC connection between device manager and policy plugin.
func (c *client) Disconnect() error {
	c.mutex.Lock()
	if c.grpc != nil {
		if err := c.grpc.Close(); err != nil {
			klog.V(2).ErrorS(err, "Failed to close grcp connection", "name", c.Name())
		}
		c.grpc = nil
	}
	c.mutex.Unlock()
	c.handler.PluginDisconnected(c.name)
	return nil
}

func (c *client) API() api.PolicyPluginClient {
	return c.client
}

func (c *client) SocketPath() string {
	return c.socket
}

func (c *client) Name() string {
	return c.name
}

// dial establishes the gRPC communication with the registered policy plugin. https://godoc.org/google.golang.org/grpc#Dial
func dial(unixSocketPath string) (api.PolicyPluginClient, *grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := grpc.DialContext(ctx, unixSocketPath,
		grpc.WithAuthority("localhost"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", addr)
		}),
	)

	if err != nil {
		return nil, nil, fmt.Errorf(errFailedToDialPolicyPlugin+" %v", err)
	}

	return api.NewPolicyPluginClient(c), c, nil
}
