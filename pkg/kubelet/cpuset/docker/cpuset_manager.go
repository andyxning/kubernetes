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

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/golang/glog"
	cadvisorapi "github.com/google/cadvisor/info/v1"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/pkg/api/v1"
	"k8s.io/kubernetes/pkg/kubelet/cpuset"
	"k8s.io/kubernetes/pkg/kubelet/dockertools"
)

type activePodsLister interface {
	// Returns a list of active pods on the node.
	GetActivePods() []*v1.Pod
}

type cpusetManager struct {
	sync.Mutex

	allNumaSets   sets.String
	allocatedNuma *podCpuSets
	cpuDetails    CPUDetails
	allCpuSets    sets.String
	allocatedCpu  *podCpuSets

	machineInfo *cadvisorapi.MachineInfo

	dockerClient     dockertools.DockerInterface
	activePodsLister activePodsLister
}

// NewCpuSetManager returns a cpusetManager that manages local cpusets.
func NewCpuSetManager(activePodsLister activePodsLister, machineInfo *cadvisorapi.MachineInfo, dockerClient dockertools.DockerInterface) (cpuset.CpuSetManager, error) {
	if dockerClient == nil {
		return nil, fmt.Errorf("invalid docker client specified")
	}
	return &cpusetManager{
		allNumaSets:      sets.NewString(),
		cpuDetails:       newCPUDetails(),
		allCpuSets:       sets.NewString(),
		machineInfo:      machineInfo,
		dockerClient:     dockerClient,
		activePodsLister: activePodsLister,
	}, nil
}

// Discover returns CPUTopology based on cadvisor node info
func (csm *cpusetManager) discover() {
	machineInfo := csm.machineInfo
	for _, socket := range machineInfo.Topology {
		csm.allNumaSets.Insert(strconv.Itoa(socket.Id))
		for _, core := range socket.Cores {
			for _, cpu := range core.Threads {
				csm.allCpuSets.Insert(strconv.Itoa(cpu))
				csm.cpuDetails[cpu] = CPUInfo{
					CoreID:   core.Id,
					SocketID: socket.Id,
				}
			}
		}
	}
}

func (csm *cpusetManager) Start() error {
	if csm.dockerClient == nil {
		return fmt.Errorf("Invalid docker client specified in CpuSet Manager")
	}
	csm.Lock()
	defer csm.Unlock()
	csm.discover()

	csm.allocatedNuma = csm.numasInUse()
	csm.allocatedCpu = csm.cpusInUse()
	return nil
}

func (csm *cpusetManager) CapacityNuma() v1.ResourceList {
	numas := resource.NewQuantity(int64(len(csm.allNumaSets)), resource.DecimalSI)
	return v1.ResourceList{
		v1.ResourceNuma: *numas,
	}
}

func (csm *cpusetManager) CapacityCpuSet() v1.ResourceList {
	cpusets := resource.NewQuantity(int64(len(csm.allCpuSets)), resource.DecimalSI)
	return v1.ResourceList{
		v1.ResourceCpuSet: *cpusets,
	}
}

func (csm *cpusetManager) AllocateCpu(pod *v1.Pod, container *v1.Container) ([]string, error) {
	cpusNeeded := container.Resources.Limits.Cpu().Value()
	if cpusNeeded == 0 {
		return []string{}, nil
	}
	csm.Lock()
	defer csm.Unlock()
	if csm.allocatedCpu == nil {
		csm.allocatedCpu = csm.cpusInUse()
	} else {
		csm.updateAllocatedCpus()
	}

	if cpusets := csm.allocatedCpu.getCpuSets(string(pod.UID), container.Name); cpusets != nil {
		glog.V(2).Infof("Found pre-allocated cpusets for container %q in Pod %q: %v", container.Name, pod.UID, cpusets.List())
		return cpusets.List(), nil
	}

	cpuInUse := csm.allocatedCpu.cpusets()

	glog.V(5).Infof("cpus in use: %v", cpuInUse.List())
	available := csm.allCpuSets.Difference(cpuInUse)
	glog.V(5).Infof("cpusets available: %v", available.List())
	if int64(available.Len()) < cpusNeeded {
		return []string{}, fmt.Errorf("requested number of cpus unavailable. Requested: %d, Available: %d", cpusNeeded, available.Len())
	}
	ret := available.UnsortedList()[:cpusNeeded]
	for _, cpu := range ret {
		csm.allocatedCpu.insert(string(pod.UID), container.Name, cpu)
	}

	return ret, nil
}

func (csm *cpusetManager) AllocateNuma(pod *v1.Pod, container *v1.Container) ([]string, error) {
	numasNeeded := container.Resources.Limits.Numa().Value()
	if numasNeeded == 0 {
		return []string{}, nil
	}
	csm.Lock()
	defer csm.Unlock()
	if csm.allocatedNuma == nil {
		csm.allocatedNuma = csm.numasInUse()
	} else {
		csm.updateAllocatedNumas()
	}

	if cpusets := csm.allocatedNuma.getCpuSets(string(pod.UID), container.Name); cpusets != nil {
		glog.V(2).Infof("Found pre-allocated cpusets for container %q in Pod %q: %v", container.Name, pod.UID, cpusets.List())
		return cpusets.List(), nil
	}

	numaInUse := csm.allocatedNuma.cpusets()
	glog.V(5).Infof("numas in use: %v", numaInUse.List())
	available := csm.allNumaSets.Difference(numaInUse)
	glog.V(5).Infof("cpusets available: %v", available.List())
	if int64(available.Len()) < numasNeeded {
		return []string{}, fmt.Errorf("requested number of numas unavailable. Requested: %d, Available: %d", numasNeeded, available.Len())
	}
	ret := available.UnsortedList()[:numasNeeded]
	for _, numa := range ret {
		csm.allocatedNuma.insert(string(pod.UID), container.Name, numa)
	}

	return ret, nil
}

func (csm *cpusetManager) updateAllocatedCpus() {
	activePods := csm.activePodsLister.GetActivePods()
	activePodUids := sets.NewString()
	for _, pod := range activePods {
		activePodUids.Insert(string(pod.UID))
	}
	allocatedPodUids := csm.allocatedCpu.pods()
	podsToBeRemoved := allocatedPodUids.Difference(activePodUids)
	glog.V(5).Infof("pods to be removed: %v", podsToBeRemoved.List())
	csm.allocatedCpu.delete(podsToBeRemoved.List())
}

func (csm *cpusetManager) updateAllocatedNumas() {
	activePods := csm.activePodsLister.GetActivePods()
	activePodUids := sets.NewString()
	for _, pod := range activePods {
		activePodUids.Insert(string(pod.UID))
	}
	allocatedPodUids := csm.allocatedNuma.pods()
	podsToBeRemoved := allocatedPodUids.Difference(activePodUids)
	glog.V(5).Infof("pods to be removed: %v", podsToBeRemoved.List())
	csm.allocatedNuma.delete(podsToBeRemoved.List())
}

func (csm *cpusetManager) numasInUse() *podCpuSets {
	pods := csm.activePodsLister.GetActivePods()
	type containerIdentifier struct {
		id   string
		name string
	}
	type podContainers struct {
		uid        string
		containers []containerIdentifier
	}
	podContainersToInspect := []podContainers{}
	for _, pod := range pods {
		containers := sets.NewString()
		for _, container := range pod.Spec.Containers {
			if !container.Resources.Limits.Numa().IsZero() {
				containers.Insert(container.Name)
			}
		}
		if containers.Len() == 0 {
			continue
		}

		var containersToInspect []containerIdentifier
		for _, container := range pod.Status.ContainerStatuses {
			if containers.Has(container.Name) {
				containersToInspect = append(containersToInspect, containerIdentifier{container.ContainerID, container.Name})
			}
		}
		podContainersToInspect = append(podContainersToInspect, podContainers{string(pod.UID), containersToInspect})
	}

	retNumas := newPodCpuSets()
	for _, podContainer := range podContainersToInspect {
		for _, containerIdentifier := range podContainer.containers {
			containerJSON, err := csm.dockerClient.InspectContainer(containerIdentifier.id)
			if err != nil {
				glog.V(3).Infof("Failed to inspect container %q in pod %q while attempting to reconcile cpusets in use", containerIdentifier.id, podContainer.uid)
				continue
			}
			cpusetMemsStr := containerJSON.HostConfig.Resources.CpusetMems

			cpusetMems := strings.Split(cpusetMemsStr, ",")

			for _, numa := range cpusetMems {
				glog.V(4).Infof("numa %q is in use by Docker Container: %q", numa, containerJSON.ID)
				retNumas.insert(podContainer.uid, containerIdentifier.name, numa)
			}
		}
	}
	return retNumas
}

func (csm *cpusetManager) cpusInUse() *podCpuSets {
	pods := csm.activePodsLister.GetActivePods()
	type containerIdentifier struct {
		id   string
		name string
	}
	type podContainers struct {
		uid        string
		containers []containerIdentifier
	}
	podContainersToInspect := []podContainers{}
	for _, pod := range pods {
		containers := sets.NewString()
		for _, container := range pod.Spec.Containers {
			if !container.Resources.Limits.CpuSet().IsZero() {
				containers.Insert(container.Name)
			}
		}
		if containers.Len() == 0 {
			continue
		}

		var containersToInspect []containerIdentifier
		for _, container := range pod.Status.ContainerStatuses {
			if containers.Has(container.Name) {
				containersToInspect = append(containersToInspect, containerIdentifier{container.ContainerID, container.Name})
			}
		}
		podContainersToInspect = append(podContainersToInspect, podContainers{string(pod.UID), containersToInspect})
	}

	retCpus := newPodCpuSets()
	for _, podContainer := range podContainersToInspect {
		for _, containerIdentifier := range podContainer.containers {
			containerJSON, err := csm.dockerClient.InspectContainer(containerIdentifier.id)
			if err != nil {
				glog.V(3).Infof("Failed to inspect container %q in pod %q while attempting to reconcile cpusets in use", containerIdentifier.id, podContainer.uid)
				continue
			}
			cpusetCpusStr := containerJSON.HostConfig.Resources.CpusetCpus

			cpusetCpus := strings.Split(cpusetCpusStr, ",")

			for _, cpu := range cpusetCpus {
				glog.V(4).Infof("cpu %q is in use by Docker Container: %q", cpu, containerJSON.ID)
				retCpus.insert(podContainer.uid, containerIdentifier.name, cpu)
			}
		}
	}
	return retCpus
}
