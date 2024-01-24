/*
Copyright 2024 The Kubernetes Authors.

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

package admission

import (
	"errors"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

const (
	ErrorReasonAllocation = "ResourceAllocationError"
)

type ResourceAllocationError struct {
	Resource     string
	Needed       int
	NUMAAffinity string
	Err          error
}

func (e ResourceAllocationError) Error() string {
	return e.Err.Error()
}

func (e ResourceAllocationError) Type() string {
	return ErrorReasonAllocation
}

func HandleResourceAllocationEvent(recorder record.EventRecorder, pod *v1.Pod, cntName, reason string, err error) {
	var ra ResourceAllocationError
	if !errors.As(err, &ra) {
		return
	}
	recorder.Eventf(pod, v1.EventTypeWarning, reason, "container %q: cannot allocate resource %s=%d NUMA affinity %v: %v", cntName, ra.Resource, ra.Needed, ra.NUMAAffinity, ra.Err)
}
