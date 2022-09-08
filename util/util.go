/*
Copyright 2021 The Kubernetes Authors.

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

package util

import (
	"encoding/json"
	appgroupv1alpha1 "github.com/diktyo-io/appgroup-api/pkg/apis/appgroup/v1alpha1"
	"github.com/diktyo-io/networktopology-api/pkg/apis/networktopology/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/klog/v2"
)

// CreateMergePatch return patch generated from original and new interfaces
func CreateMergePatch(original, new interface{}) ([]byte, error) {
	pvByte, err := json.Marshal(original)
	if err != nil {
		return nil, err
	}
	cloneByte, err := json.Marshal(new)
	if err != nil {
		return nil, err
	}
	patch, err := strategicpatch.CreateTwoWayMergePatch(pvByte, cloneByte, original)
	if err != nil {
		return nil, err
	}
	return patch, nil
}

// GetPodAppGroupLabel : get AppGroup from pod annotations
func GetPodAppGroupLabel(pod *v1.Pod) string {
	return pod.Labels[appgroupv1alpha1.AppGroupLabel]
}

// GetPodAppGroupSelector : get Workload Selector from pod annotations
func GetPodAppGroupSelector(pod *v1.Pod) string {
	return pod.Labels[appgroupv1alpha1.AppGroupSelectorLabel]
}

// key for map concerning network costs (origin / destinations)
type CostKey struct {
	Origin      string
	Destination string
}

type ScheduledInfo struct {
	// Pod Name
	Name string

	// Pod AppGroup Selector
	Selector string

	// Replica ID
	ReplicaID string

	// Hostname
	Hostname string
}

type ScheduledList []ScheduledInfo

func GetNodeRegion(node *v1.Node) string {
	labels := node.Labels
	if labels == nil {
		return ""
	}

	zone, _ := labels[v1.LabelTopologyRegion]
	if zone == "" {
		return ""
	}

	return zone
}

func GetNodeZone(node *v1.Node) string {
	labels := node.Labels
	if labels == nil {
		return ""
	}

	region, _ := labels[v1.LabelTopologyZone]
	if region == "" {
		return ""
	}

	return region
}

// Sort TopologyList by TopologyKey
type ByTopologyKey v1alpha1.TopologyList

func (s ByTopologyKey) Len() int {
	return len(s)
}

func (s ByTopologyKey) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s ByTopologyKey) Less(i, j int) bool {
	return s[i].TopologyKey < s[j].TopologyKey
}

// Sort OriginList by Origin (e.g., Region Name, Zone Name)
type ByOrigin v1alpha1.OriginList

func (s ByOrigin) Len() int {
	return len(s)
}

func (s ByOrigin) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s ByOrigin) Less(i, j int) bool {
	return s[i].Origin < s[j].Origin
}

// Sort CostList by Destination (e.g., Region Name, Zone Name)
type ByDestination v1alpha1.CostList

func (s ByDestination) Len() int {
	return len(s)
}

func (s ByDestination) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s ByDestination) Less(i, j int) bool {
	return s[i].Destination < s[j].Destination
}

func FindOriginCosts(originList []v1alpha1.OriginInfo, origin string) []v1alpha1.CostInfo {
	low := 0
	high := len(originList) - 1

	for low <= high {
		mid := (low + high) / 2
		if originList[mid].Origin == origin {
			return originList[mid].CostList // Return the CostList
		} else if originList[mid].Origin < origin {
			low = mid + 1
		} else if originList[mid].Origin > origin {
			high = mid - 1
		}
	}
	// Costs were not found
	return []v1alpha1.CostInfo{}
}

// assignedPod selects pods that are assigned (scheduled and running).
func AssignedPod(pod *v1.Pod) bool {
	return len(pod.Spec.NodeName) != 0
}

// GetDependencyList : get workload dependencies established in the AppGroup CR
func GetDependencyList(pod *v1.Pod, ag *appgroupv1alpha1.AppGroup) []appgroupv1alpha1.DependenciesInfo {

	// Check Dependencies of the given pod
	var dependencyList []appgroupv1alpha1.DependenciesInfo

	// Get Labels of the given pod
	podLabels := pod.GetLabels()

	for _, w := range ag.Spec.Workloads {
		if w.Workload.Selector == podLabels[appgroupv1alpha1.AppGroupSelectorLabel] {
			for _, dependency := range w.Dependencies {
				dependencyList = append(dependencyList, dependency)
			}
		}
	}
	klog.V(6).Info("dependencyList: ", dependencyList)

	// Return the dependencyList
	return dependencyList
}

// GetScheduledList : get Pods already scheduled in the cluster for that specific AppGroup
func GetScheduledList(pods []*v1.Pod) ScheduledList {
	// scheduledList: Deployment name, replicaID, hostname
	scheduledList := ScheduledList{}

	for _, p := range pods {
		if AssignedPod(p) {
			scheduledInfo := ScheduledInfo{
				Name:      p.Name,
				Selector:  GetPodAppGroupSelector(p),
				ReplicaID: string(p.GetUID()),
				Hostname:  p.Spec.NodeName,
			}
			scheduledList = append(scheduledList, scheduledInfo)
		}
	}
	return scheduledList
}
