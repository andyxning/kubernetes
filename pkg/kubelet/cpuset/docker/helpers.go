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

package docker

import "k8s.io/apimachinery/pkg/util/sets"

type CPUInfo struct {
	SocketID int
	CoreID   int
}
type CPUDetails map[int]CPUInfo

type containerToCpuSet map[string]sets.String

type podCpuSets struct {
	podCpuSetMapping map[string]containerToCpuSet
}

func newCPUDetails() CPUDetails {
	return CPUDetails{}
}

func newPodCpuSets() *podCpuSets {
	return &podCpuSets{
		podCpuSetMapping: make(map[string]containerToCpuSet),
	}
}

func (pcpuset *podCpuSets) pods() sets.String {
	ret := sets.NewString()
	for k := range pcpuset.podCpuSetMapping {
		ret.Insert(k)
	}
	return ret
}

func (pcpuset *podCpuSets) insert(podUID, contName string, device string) {
	if _, exists := pcpuset.podCpuSetMapping[podUID]; !exists {
		pcpuset.podCpuSetMapping[podUID] = make(containerToCpuSet)
	}
	if _, exists := pcpuset.podCpuSetMapping[podUID][contName]; !exists {
		pcpuset.podCpuSetMapping[podUID][contName] = sets.NewString()
	}
	pcpuset.podCpuSetMapping[podUID][contName].Insert(device)
}

func (pcpuset *podCpuSets) getCpuSets(podUID, contName string) sets.String {
	containers, exists := pcpuset.podCpuSetMapping[podUID]
	if !exists {
		return nil
	}
	pcpusets, exists := containers[contName]
	if !exists {
		return nil
	}
	return pcpusets
}

func (pcpuset *podCpuSets) delete(pods []string) {
	for _, uid := range pods {
		delete(pcpuset.podCpuSetMapping, uid)
	}
}

func (pcpuset *podCpuSets) cpusets() sets.String {
	ret := sets.NewString()
	for _, containerToCpuSet := range pcpuset.podCpuSetMapping {
		for _, cpuSet := range containerToCpuSet {
			ret = ret.Union(cpuSet)
		}
	}
	return ret
}
