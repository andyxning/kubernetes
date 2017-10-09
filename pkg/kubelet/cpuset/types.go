/*
Copyright 2017 The Kubernetes Authors.

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

package cpuset

import "k8s.io/kubernetes/pkg/api/v1"

// CpuSetManager manages cpuset on a local node.
// Implementations are expected to be thread safe.
type CpuSetManager interface {
	// Start logically initializes CpuSetManager
	Start() error
	// Capacity returns the total number of numas on the node.
	CapacityNuma() v1.ResourceList
	// Capacity returns the total number of cpusets on the node.
	CapacityCpuset() v1.ResourceList
	// AllocateMem attempts to allocate numa for input container.
	// Returns paths to allocated GPUs and nil on success.
	// Returns an error on failure.
	AllocateNuma(*v1.Pod, *v1.Container) ([]string, error)
	// AllocateCpu attempts to allocate cpu for input container.
	// Returns paths to allocated GPUs and nil on success.
	// Returns an error on failure.
	AllocateCpu(*v1.Pod, *v1.Container) ([]string, error)
}
