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

// RegistrationHandler is an interface for handling policy plugin registration
// and plugin directory cleanup.
type RegistrationHandler interface {
	CleanupPluginDirectory(string) error
}

// ClientHandler is an interface for handling policy plugin connections.
type ClientHandler interface {
	PluginConnected(string, PolicyPlugin) error
	PluginDisconnected(string)
}

// TODO: evaluate whether we need these error definitions.
const (
	// errFailedToDialPolicyPlugin is the error raised when the policy plugin could not be
	// reached on the registered socket
	errFailedToDialPolicyPlugin = "failed to dial policy plugin:"
	// errUnsupportedVersion is the error raised when the policy plugin uses an API version not
	// supported by the Kubelet registry
	errUnsupportedVersion = "requested API version %q is not supported by kubelet. Supported version is %q"
	// errBadSocket is the error raised when the registry socket path is not absolute
	errBadSocket = "bad socketPath, must be an absolute path:"
)
