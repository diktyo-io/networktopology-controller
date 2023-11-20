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

package controller

import (
	"context"
	"fmt"
	appgroupv1alpha1 "github.com/diktyo-io/appgroup-api/pkg/apis/appgroup/v1alpha1"
	aginformers "github.com/diktyo-io/appgroup-api/pkg/generated/informers/externalversions/appgroup/v1alpha1"
	ntinformers "github.com/diktyo-io/networktopology-api/pkg/generated/informers/externalversions/networktopology/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	corelister "k8s.io/client-go/listers/core/v1"
	klog "k8s.io/klog/v2"
	util "networktopology-controller/util"
	"reflect"
	"strconv"
	"sync"
	"time"

	aglisters "github.com/diktyo-io/appgroup-api/pkg/generated/listers/appgroup/v1alpha1"
	v1alpha1 "github.com/diktyo-io/networktopology-api/pkg/apis/networktopology/v1alpha1"

	clientset "github.com/diktyo-io/networktopology-api/pkg/generated/clientset/versioned"
	ntlisters "github.com/diktyo-io/networktopology-api/pkg/generated/listers/networktopology/v1alpha1"

	v1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformer "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"sort"
)

// NetworkTopologyController is a controller that process Network Topology using provided Handler interface
type NetworkTopologyController struct {
	eventRecorder         record.EventRecorder
	ntQueue               workqueue.RateLimitingInterface
	ntLister              ntlisters.NetworkTopologyLister
	agLister              aglisters.AppGroupLister
	nodeLister            corelister.NodeLister
	podLister             corelister.PodLister
	configmapLister       corelister.ConfigMapLister
	ntListerSynced        cache.InformerSynced
	nodeListerSynced      cache.InformerSynced
	podListerSynced       cache.InformerSynced
	configmapListerSynced cache.InformerSynced
	ntClient              clientset.Interface
	lock                  sync.RWMutex // lock for network graph and cost calculation.
	nodeCount             int64        // Number of nodes in the cluster.
	regionGraph           *util.Graph  // Network Graph for region cost calculation.
	zoneGraph             *util.Graph  // Network Graph for zone cost calculation.
	nodeGraph             *util.Graph  // Network Graph for node cost calculation.
	segmentGraph          *util.Graph  // Network Graph for segment cost calculation.
	topologyMap           map[util.TopologyKey]bool
	ZoneMap               map[util.ZoneKey]bool
	SegmentMap            map[util.SegmentKey]bool
	BandwidthAllocatable  map[util.CostKey]resource.Quantity
}

// NewNetworkTopologyController returns a new *NewNetworkTopologyController
func NewNetworkTopologyController(client kubernetes.Interface,
	ntInformer ntinformers.NetworkTopologyInformer,
	agInformer aginformers.AppGroupInformer,
	nodeInformer coreinformer.NodeInformer,
	podInformer coreinformer.PodInformer,
	comfigmapInformer coreinformer.ConfigMapInformer,
	ntClient clientset.Interface) *NetworkTopologyController {
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&corev1.EventSinkImpl{Interface: client.CoreV1().Events(v1.NamespaceAll)})

	ctrl := &NetworkTopologyController{
		eventRecorder: broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "NetworkTopologyController"}),
		ntQueue:       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "NetworkTopology"),
	}

	// NetworkTopology Informer
	klog.V(5).InfoS("Setting up NetworkTopology event handlers")
	ntInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    ctrl.ntAdded,
		UpdateFunc: ctrl.ntUpdated,
		DeleteFunc: ctrl.ntDeleted,
	})

	// Node Informer
	klog.V(5).InfoS("Setting up Node event handlers")
	nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    ctrl.nodeAdded,
		UpdateFunc: ctrl.nodeUpdated,
		DeleteFunc: ctrl.nodeDeleted,
	})

	// Pod Informer
	klog.V(5).InfoS("Setting up Pod event handlers")
	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    ctrl.podAdded,
		UpdateFunc: ctrl.podUpdated,
		DeleteFunc: ctrl.podDeleted,
	})

	ctrl.ntLister = ntInformer.Lister()
	ctrl.agLister = agInformer.Lister()
	ctrl.nodeLister = nodeInformer.Lister()
	ctrl.podLister = podInformer.Lister()
	ctrl.configmapLister = comfigmapInformer.Lister()
	ctrl.ntListerSynced = ntInformer.Informer().HasSynced
	ctrl.nodeListerSynced = nodeInformer.Informer().HasSynced
	ctrl.podListerSynced = podInformer.Informer().HasSynced
	ctrl.configmapListerSynced = comfigmapInformer.Informer().HasSynced
	ctrl.ntClient = ntClient

	ctrl.regionGraph = util.NewGraph()
	ctrl.zoneGraph = util.NewGraph()
	ctrl.nodeGraph = util.NewGraph()
	ctrl.segmentGraph = util.NewGraph()

	ctrl.topologyMap = make(map[util.TopologyKey]bool)
	ctrl.ZoneMap = make(map[util.ZoneKey]bool)
	ctrl.SegmentMap = make(map[util.SegmentKey]bool)

	//ctrl.BandwidthCapacity = make(map[util.CostKey]resource.Quantity)
	ctrl.BandwidthAllocatable = make(map[util.CostKey]resource.Quantity)

	return ctrl
}

// Run starts listening on channel events
func (ctrl *NetworkTopologyController) Run(workers int, stopCh <-chan struct{}) {
	defer ctrl.ntQueue.ShutDown()

	klog.InfoS("Starting Network Topology controller")
	defer klog.InfoS("Shutting Network Topology controller")

	if !cache.WaitForCacheSync(stopCh, ctrl.ntListerSynced, ctrl.nodeListerSynced) {
		klog.Error("Cannot sync caches")
		return
	}

	klog.InfoS("Network Topology sync finished")

	for i := 0; i < workers; i++ {
		go wait.Until(ctrl.worker, time.Second, stopCh)
	}
	<-stopCh
}

// ntAdded reacts to a NT creation
func (ctrl *NetworkTopologyController) ntAdded(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}

	klog.V(5).InfoS("Enqueue Network Topology ", "network Topology", key)
	ctrl.ntQueue.Add(key)
}

// ntUpdated reacts to a NT update
func (ctrl *NetworkTopologyController) ntUpdated(old, new interface{}) {
	ctrl.ntAdded(new)
}

// ntDeleted reacts to a NetworkTopology deletion
func (ctrl *NetworkTopologyController) ntDeleted(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}
	klog.V(5).InfoS("Enqueue deleted network topology key", "networkTopology", key)
	ctrl.ntQueue.AddRateLimited(key)
}

// nodeAdded reacts to a node addition
func (ctrl *NetworkTopologyController) nodeAdded(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if !ok {
		klog.Error("unexpected object type in node added")
		return
	}

	region := util.GetNodeLabel(node, v1.LabelTopologyRegion)
	zone := util.GetNodeLabel(node, v1.LabelTopologyZone)

	func() {
		ctrl.lock.Lock()
		defer ctrl.lock.Unlock()
		// Add node to total
		ctrl.nodeCount++

		if region != "" && zone != "" {
			// Add region to graph
			// ctrl.regionGraph.AddEdge(region, region, 0)

			// Add zone to graph
			// ctrl.zoneGraph.AddEdge(zone, zone, 0)

			// Add the region / zone to the map
			ctrl.topologyMap[util.TopologyKey{
				Region: region,
				Zone:   zone}] = true
		}

		// Add node to graph
		// ctrl.nodeGraph.AddEdge(node.Name, node.Name, 0)

	}()
	klog.V(5).Infof("Added node %v - Total node count: %v", node.Name, ctrl.nodeCount)
	return
}

// nodeUpdated reacts to a node update
func (ctrl *NetworkTopologyController) nodeUpdated(old, new interface{}) {
	// Check if zone label has been modified ...
	newNode, ok := new.(*v1.Node)
	if !ok {
		klog.Error("unexpected object type in node added")
		return
	}

	oldNode, err := old.(*v1.Node)
	if !err {
		klog.Error("unexpected object type in node added")
		return
	}

	var oldRegion string
	var oldZone string
	if old != nil {
		oldRegion = util.GetNodeLabel(oldNode, v1.LabelTopologyRegion)
		oldZone = util.GetNodeLabel(oldNode, v1.LabelTopologyZone)
	}

	newRegion := util.GetNodeLabel(newNode, v1.LabelTopologyRegion)
	newZone := util.GetNodeLabel(newNode, v1.LabelTopologyZone)

	// If the zone of the node did not changed, we don't need to do anything.
	if oldZone == newZone && oldRegion == newRegion {
		return
	}
	// Otherwise update zone of the given Node
	ctrl.nodeDeleted(old)
	ctrl.nodeAdded(new)
}

// nodeDeleted reacts to a node removal
func (ctrl *NetworkTopologyController) nodeDeleted(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if !ok {
		klog.Error("unexpected object type in node deleted")
		return
	}

	_, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}

	func() {
		ctrl.lock.Lock()
		defer ctrl.lock.Unlock()
		// Remove node from total
		ctrl.nodeCount--

		// Remove all Edges from graph
		// ctrl.nodeGraph.RemoveEdge(node.Name)
	}()

	klog.V(5).Infof("Removed node %v - Total node count: %v", node.Name, ctrl.nodeCount)
}

// podAdded reacts to a Pod creation
func (ctrl *NetworkTopologyController) podAdded(obj interface{}) {
	pod := obj.(*v1.Pod)
	agName := util.GetPodAppGroupLabel(pod)
	if len(agName) == 0 {
		return
	}

	ag, err := ctrl.agLister.AppGroups(pod.Namespace).Get(agName)
	if err != nil {
		klog.ErrorS(err, "Error retrieving AppGroup...")
		return
	}

	klog.V(5).InfoS("Pod's App group: ", "AppGroup", klog.KObj(ag), "pod", klog.KObj(pod))

	// Get Dependencies of the given pod
	dependencyList := util.GetDependencyList(pod, ag)
	klog.V(5).InfoS("dependencyList: ", dependencyList)

	// If the pod has no dependencies, return
	if dependencyList == nil {
		return
	}

	// Get pods from lister
	selector := labels.Set(map[string]string{appgroupv1alpha1.AppGroupLabel: agName}).AsSelector()
	pods, err := ctrl.podLister.List(selector)
	if err != nil {
		klog.ErrorS(err, "Getting deployed pods from lister...")
		return
	}

	// No pods yet allocated...
	if pods == nil {
		return
	}

	// Get Pods already scheduled: Deployment name, replicaID, hostname
	scheduledList := util.GetScheduledList(pods)
	klog.V(5).Info("scheduledList: ", scheduledList)

	// Check if pods already available
	if scheduledList == nil {
		return
	}

	// Get Node from pod.Spec.Nodename
	hostname, err := ctrl.nodeLister.Get(pod.Spec.NodeName)
	if err != nil {
		klog.ErrorS(err, "Getting pod hostname from nodeLister...")
		return
	}

	// Retrieve Region and Zone from node
	region := util.GetNodeLabel(hostname, v1.LabelTopologyRegion)
	zone := util.GetNodeLabel(hostname, v1.LabelTopologyZone)

	// reserve bandwidth
	for _, podAllocated := range scheduledList { // For each pod already allocated
		if podAllocated.Hostname != "" { // if already updated by the controller
			for _, d := range dependencyList { // For each pod dependency
				if podAllocated.Selector == d.Workload.Selector { // If the pod allocated is an established dependency
					if podAllocated.Hostname == pod.Spec.NodeName { // If the pod's hostname is the same
						klog.V(5).Info("[Pod added] Same Hostname do nothing for this dependency... ")
					} else { // If Nodes are not the same
						// Get NodeInfo from pod Hostname
						podHostname, err := ctrl.nodeLister.Get(podAllocated.Hostname)
						if err != nil {
							klog.ErrorS(err, "Getting pod hostname from nodeLister...")
							return
						}
						// Get zone and region from Pod Hostname
						regionPodHostname := util.GetNodeLabel(podHostname, v1.LabelTopologyRegion)
						zonePodHostname := util.GetNodeLabel(podHostname, v1.LabelTopologyZone)

						if regionPodHostname == "" && zonePodHostname == "" { // Node has no zone and region defined
							klog.V(5).Info("[Pod added] Null region/zone do nothing for this dependency... ")
						} else if region == regionPodHostname { // If Nodes belong to the same region
							if zone == zonePodHostname { // If Nodes belong to the same zone
								klog.Info("[Pod added] Same Zone do nothing for this dependency... ")
							} else { // belong to a different zone
								value, ok := ctrl.BandwidthAllocatable[util.CostKey{ // Retrieve the current allocatable bandwidth from the map (origin: zone, destination: pod zoneHostname)
									Origin:      zone,
									Destination: zonePodHostname,
								}]
								if ok {
									value.Add(d.MinBandwidth)
									ctrl.BandwidthAllocatable[util.CostKey{ // Add the updated bandwidth to the map
										Origin:      zone,
										Destination: zonePodHostname}] = value
								} else {
									klog.V(5).Infof("[zones] Bandwidth allocatable not found in map... add minBandwidth")
									capacity := *resource.NewQuantity(0, resource.DecimalSI)
									capacity.Add(d.MinBandwidth)

									ctrl.BandwidthAllocatable[util.CostKey{ // Add the updated bandwidth to the map
										Origin:      zone,
										Destination: zonePodHostname}] = capacity
								}
							}
						} else { // belong to a different region
							value, ok := ctrl.BandwidthAllocatable[util.CostKey{ // Retrieve the current allocable bandwidth from the map (origin: region, destination: pod regionHostname)
								Origin:      region,
								Destination: regionPodHostname,
							}]
							if ok {
								value.Add(d.MinBandwidth)
								ctrl.BandwidthAllocatable[util.CostKey{ // Add the updated bandwidth to the map
									Origin:      region,
									Destination: regionPodHostname}] = value
							} else {
								klog.V(5).Infof("[regions] Bandwidth allocatable not found in map... add minBandwidth")
								capacity := *resource.NewQuantity(0, resource.DecimalSI)
								capacity.Add(d.MinBandwidth)

								ctrl.BandwidthAllocatable[util.CostKey{ // Add the updated bandwidth to the map
									Origin:      region,
									Destination: regionPodHostname}] = capacity
							}
						}
					}
				}
			}
		}
	}
}

// podDeleted reacts to a pod delete
func (ctrl *NetworkTopologyController) podDeleted(obj interface{}) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		klog.Error("unexpected object type in pod deleted")
		return
	}

	_, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}

	agName := util.GetPodAppGroupLabel(pod)
	if len(agName) == 0 {
		return
	}

	func() {
		ctrl.lock.Lock()
		defer ctrl.lock.Unlock()

		ag, err := ctrl.agLister.AppGroups(pod.Namespace).Get(agName)
		if err != nil {
			klog.ErrorS(err, "Error retrieving AppGroup...")
			return
		}

		klog.V(5).InfoS("[Pod deleted] Pod's App group: ", "AppGroup", klog.KObj(ag), "pod", klog.KObj(pod))

		// Get Dependencies of the given pod
		dependencyList := util.GetDependencyList(pod, ag)
		klog.V(5).Info("[Pod deleted] dependencyList: ", dependencyList)

		// If the pod has no dependencies, return
		if dependencyList == nil {
			return
		}

		// Get pods from lister
		selector := labels.Set(map[string]string{appgroupv1alpha1.AppGroupLabel: agName}).AsSelector()
		pods, err := ctrl.podLister.List(selector)
		if err != nil {
			klog.ErrorS(err, "[Pod deleted] Getting deployed pods from lister...")
			return
		}

		// No pods yet allocated...
		if pods == nil {
			return
		}

		// Get Pods already scheduled: Deployment name, replicaID, hostname
		scheduledList := util.GetScheduledList(pods)
		klog.V(5).Info("scheduledList: ", scheduledList)

		// Check if pods already available
		if scheduledList == nil {
			return
		}

		// Get Node from pod.Spec.Nodename
		hostname, err := ctrl.nodeLister.Get(pod.Spec.NodeName)
		if err != nil {
			klog.ErrorS(err, "[Pod deleted] Getting pod hostname from nodeLister...")
			return
		}

		// Retrieve Region and Zone from node
		region := util.GetNodeLabel(hostname, v1.LabelTopologyRegion)
		zone := util.GetNodeLabel(hostname, v1.LabelTopologyRegion)

		// delete reserved bandwidth
		for _, podAllocated := range scheduledList { // For each pod already allocated
			if podAllocated.Hostname != "" { // if already updated by the controller
				for _, d := range dependencyList { // For each pod dependency
					if podAllocated.Selector == d.Workload.Selector { // If the pod allocated is an established dependency
						if podAllocated.Hostname == pod.Spec.NodeName { // If the pod's hostname is the same
							klog.V(5).Info("[Pod deleted] Same hostname do nothing for this dependency... ")
						} else { // If Nodes are not the same
							// Get NodeInfo from pod Hostname
							podHostname, err := ctrl.nodeLister.Get(podAllocated.Hostname)
							if err != nil {
								klog.ErrorS(err, "Getting pod hostname from nodeLister...")
								return
							}
							// Get zone and region from Pod Hostname
							regionPodHostname := util.GetNodeLabel(podHostname, v1.LabelTopologyRegion)
							zonePodHostname := util.GetNodeLabel(podHostname, v1.LabelTopologyZone)

							if regionPodHostname == "" && zonePodHostname == "" { // Node has no zone and region defined
								klog.V(5).Info("[Pod deleted] Null region/zone do nothing for this dependency... ")
							} else if region == regionPodHostname { // If Nodes belong to the same region
								if zone == zonePodHostname { // If Nodes belong to the same zone
									klog.V(5).Info("[Pod deleted] Same Zone do nothing for this dependency... ")
								} else { // belong to a different zone
									value, ok := ctrl.BandwidthAllocatable[util.CostKey{ // Retrieve the current allocatable bandwidth from the map (origin: zone, destination: pod zoneHostname)
										Origin:      zone,
										Destination: zonePodHostname,
									}]
									if ok {
										value.Sub(d.MinBandwidth)
										ctrl.BandwidthAllocatable[util.CostKey{ // Add the updated bandwidth to the map
											Origin:      zone,
											Destination: zonePodHostname}] = value
									} else {
										klog.V(5).Infof("[zones] Bandwidth allocatable not found in map... add 0")
										capacity := *resource.NewQuantity(0, resource.DecimalSI)

										ctrl.BandwidthAllocatable[util.CostKey{ // Add the updated bandwidth to the map
											Origin:      zone,
											Destination: zonePodHostname}] = capacity
									}
								}
							} else { // belong to a different region
								value, ok := ctrl.BandwidthAllocatable[util.CostKey{ // Retrieve the current allocable bandwidth from the map (origin: region, destination: pod regionHostname)
									Origin:      region,
									Destination: regionPodHostname,
								}]
								if ok {
									value.Sub(d.MinBandwidth)
									ctrl.BandwidthAllocatable[util.CostKey{ // Add the updated bandwidth to the map
										Origin:      region,
										Destination: regionPodHostname}] = value
								} else {
									klog.V(5).Infof("[regions] Bandwidth allocatable not found in map... add 0")
									capacity := *resource.NewQuantity(0, resource.DecimalSI)

									ctrl.BandwidthAllocatable[util.CostKey{ // Add the updated bandwidth to the map
										Origin:      region,
										Destination: regionPodHostname}] = capacity
								}
							}
						}
					}
				}
			}
		}
	}()
	ctrl.podAdded(obj)
}

// pgUpdated reacts to a PG update
func (ctrl *NetworkTopologyController) podUpdated(old, new interface{}) {
	ctrl.podAdded(new)
}

func (ctrl *NetworkTopologyController) worker() {
	for ctrl.processNextWorkItem() {
	}
}

// processNextWorkItem deals with one key off the queue.  It returns false when it's time to quit.
func (ctrl *NetworkTopologyController) processNextWorkItem() bool {
	keyObj, quit := ctrl.ntQueue.Get()
	if quit {
		return false
	}
	defer ctrl.ntQueue.Done(keyObj)

	key, ok := keyObj.(string)
	if !ok {
		ctrl.ntQueue.Forget(keyObj)
		runtime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", keyObj))
		return true
	}
	if err := ctrl.syncHandler(key); err != nil {
		runtime.HandleError(err)
		klog.ErrorS(err, "Error syncing network topology", "networkTopology", key)
		return true
	}
	return true
}

// syncHandle syncs network topology and convert status
func (ctrl *NetworkTopologyController) syncHandler(key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}
	defer func() {
		if err != nil {
			ctrl.ntQueue.AddRateLimited(key)
			return
		}
	}()
	nt, err := ctrl.ntLister.NetworkTopologies(namespace).Get(name)
	if apierrs.IsNotFound(err) {
		klog.V(5).InfoS("Network Topology has been deleted", "networkTopology", key)
		return nil
	}
	if err != nil {
		klog.V(3).ErrorS(err, "Unable to retrieve Network Topology from store", "networkTopology", key)
		return err
	}

	ntCopy := nt.DeepCopy()

	nodes, err := ctrl.nodeLister.List(labels.Everything())
	if err != nil {
		klog.ErrorS(err, "List nodes failed during syncHandler", "networkTopology", klog.KObj(ntCopy))
		return err
	}

	configmap, err := ctrl.configmapLister.ConfigMaps(namespace).Get(ntCopy.Spec.ConfigmapName)
	if apierrs.IsNotFound(err) {
		klog.V(5).InfoS("ConfigMap has been deleted", "networkTopology", key)
		return nil
	}
	if err != nil {
		klog.V(3).ErrorS(err, "Unable to retrieve ConfigMap from store", "networkTopology", key)
		return err
	}

	klog.Infof("ConfigMap %v retrieved...", configmap.Name)

	// Update Status of Network Topology CRD

	// NodeCount
	ctrl.lock.Lock()
	ntCopy.Status.NodeCount = ctrl.nodeCount
	ctrl.lock.Unlock()

	// Weights
	ctrl.lock.Lock()

	if ntCopy.Status.WeightCalculationTime.IsZero() {
		klog.V(5).InfoS("Initial Calculation of Weight List...")

		var manualCosts v1alpha1.TopologyList
		var manualRegionCosts v1alpha1.OriginList
		var manualZoneCosts v1alpha1.OriginList
		var manualSegmentCosts v1alpha1.OriginList

		for _, w := range ntCopy.Spec.Weights {
			if w.Name == "UserDefined" {
				manualCosts = w.TopologyList
				for _, c := range w.TopologyList {
					if c.TopologyKey == v1alpha1.NetworkTopologyRegion {
						manualRegionCosts = c.OriginList
					}
					if c.TopologyKey == v1alpha1.NetworkTopologyZone {
						manualZoneCosts = c.OriginList
					}
					if c.TopologyKey == v1alpha1.NetworkTopologySegment {
						manualSegmentCosts = c.OriginList
					}
				}
			}
		}

		err := updateGraph(ctrl, nodes, configmap)
		if err != nil {
			runtime.HandleError(err)
			klog.ErrorS(err, "Error updating Weight List", "networkTopology", key)
			return err
		}

		klog.V(5).Infof("Graph: %v", ctrl.nodeGraph)

		weights := v1alpha1.WeightList{}

		weights = append(weights, v1alpha1.WeightInfo{
			Name:         "UserDefined",
			TopologyList: manualCosts,
		})

		weights = append(weights, v1alpha1.WeightInfo{
			Name:         v1alpha1.NetworkTopologyNetperfCosts,
			TopologyList: getTopologyList(ctrl, nodes, manualRegionCosts, manualZoneCosts, manualSegmentCosts),
		})

		klog.V(5).InfoS("Calculated Weights", "weights", weights)
		ntCopy.Spec.Weights = weights

		ntCopy.Status.WeightCalculationTime = metav1.Time{Time: time.Now()}

	} else if time.Now().Sub(ntCopy.Status.WeightCalculationTime.Time) > 5*time.Minute {
		klog.V(5).InfoS("Recalculation of Weight List... Every 5 min...")

		var manualCosts v1alpha1.TopologyList
		var manualRegionCosts v1alpha1.OriginList
		var manualZoneCosts v1alpha1.OriginList
		var manualSegmentCosts v1alpha1.OriginList

		for _, w := range ntCopy.Spec.Weights {
			if w.Name == "UserDefined" {
				manualCosts = w.TopologyList
				for _, c := range w.TopologyList {
					if c.TopologyKey == v1alpha1.NetworkTopologyRegion {
						manualRegionCosts = c.OriginList
					}
					if c.TopologyKey == v1alpha1.NetworkTopologyZone {
						manualZoneCosts = c.OriginList
					}
					if c.TopologyKey == v1alpha1.NetworkTopologySegment {
						manualSegmentCosts = c.OriginList
					}
				}
			}
		}

		err := updateGraph(ctrl, nodes, configmap)
		if err != nil {
			runtime.HandleError(err)
			klog.ErrorS(err, "Error updating Weight List", "networkTopology", key)
			return err
		}

		weights := v1alpha1.WeightList{}

		weights = append(weights, v1alpha1.WeightInfo{
			Name:         "UserDefined",
			TopologyList: manualCosts,
		})

		weights = append(weights, v1alpha1.WeightInfo{
			Name:         v1alpha1.NetworkTopologyNetperfCosts,
			TopologyList: getTopologyList(ctrl, nodes, manualRegionCosts, manualZoneCosts, manualSegmentCosts),
		})

		klog.V(5).InfoS("Calculated Weights", "weights", weights)
		ntCopy.Spec.Weights = weights
		ntCopy.Status.WeightCalculationTime = metav1.Time{Time: time.Now()}
	}

	ctrl.lock.Unlock()

	// Patch ntCopy
	err = ctrl.patchNetworkTopology(nt, ntCopy)
	if err == nil {
		ctrl.ntQueue.Forget(nt)
	}
	return err
}

func (ctrl *NetworkTopologyController) patchNetworkTopology(old, new *v1alpha1.NetworkTopology) error {
	if !reflect.DeepEqual(old, new) {
		patch, err := util.CreateMergePatch(old, new)
		if err != nil {
			return err
		}

		_, err = ctrl.ntClient.NetworktopologyV1alpha1().NetworkTopologies(old.Namespace).Patch(context.TODO(), old.Name, types.MergePatchType,
			patch, metav1.PatchOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

// Update the weights based on latency measurements saved in the configmap
func updateGraph(ctrl *NetworkTopologyController, nodes []*v1.Node, configmap *v1.ConfigMap) error {
	klog.InfoS("NetworkTopology SyncHandler: Update costs in the network graph... ")

	// Rebuild the graph
	ctrl.regionGraph = util.NewGraph()
	ctrl.zoneGraph = util.NewGraph()
	ctrl.nodeGraph = util.NewGraph()
	ctrl.segmentGraph = util.NewGraph()

	for _, n1 := range nodes {
		r1 := util.GetNodeLabel(n1, v1.LabelTopologyRegion)
		z1 := util.GetNodeLabel(n1, v1.LabelTopologyZone)
		s1 := util.GetNodeLabel(n1, string(v1alpha1.NetworkTopologySegment))

		for _, n2 := range nodes {
			// Avoid adding costs for origin = destination
			if n1.Name != n2.Name {
				r2 := util.GetNodeLabel(n2, v1.LabelTopologyRegion)
				z2 := util.GetNodeLabel(n2, v1.LabelTopologyZone)
				s2 := util.GetNodeLabel(n2, string(v1alpha1.NetworkTopologySegment))

				klog.V(5).InfoS("[updateGraph] ",
					"N1_hostname", n1.Name, "N2_hostname", n2.Name,
					"N1_region", r1, "N2_region", r2,
					"N1_Zone", z1, "N2_Zone", z2,
					"N1_Segment", s1, "N2_Segment", s2)

				// get cost from configmap
				key := util.GetConfigmapCostQuery(n1.Name, n2.Name)

				klog.V(5).InfoS("data:", "key", key, "configmap.Data", configmap.Data)

				cost, err := strconv.Atoi(configmap.Data[key])
				if err != nil {
					klog.ErrorS(err, "Error converting cost...")
				}

				klog.V(5).InfoS("retrieved_cost:", "cost", cost)

				// Update Cost in the node and segment graph
				ctrl.nodeGraph.AddEdge(n1.Name, n2.Name, cost)
				ctrl.segmentGraph.AddEdge(s1, s2, cost)

				// Add segment key to map
				ctrl.SegmentMap[util.SegmentKey{
					S1: s1,
					S2: s2,
				}] = true

				if r1 != r2 { // Different region
					current, err := ctrl.regionGraph.GetPath(r1, r2)
					if err != nil { // add average cost!
						cost = (cost + current) / 2
						ctrl.regionGraph.AddEdge(r1, r2, cost)
					}
					ctrl.regionGraph.AddEdge(r1, r2, cost)
				} else if z1 != z2 { // Same region Different zone
					// Add zone key to map
					ctrl.ZoneMap[util.ZoneKey{
						Z1: z1,
						Z2: z2,
					}] = true

					current, err := ctrl.zoneGraph.GetPath(z1, z2)
					if err != nil { // Add average cost
						cost = (cost + current) / 2
						ctrl.zoneGraph.AddEdge(z1, z2, cost)
					}
					ctrl.zoneGraph.AddEdge(z1, z2, cost)
				}
			}
		}
	}
	return nil
}

func getTopologyList(ctrl *NetworkTopologyController, nodes []*v1.Node,
	manualRegionCosts v1alpha1.OriginList, manualZoneCosts v1alpha1.OriginList, manualSegmentCosts v1alpha1.OriginList) v1alpha1.TopologyList {
	var topologyList v1alpha1.TopologyList

	topologyList = append(topologyList, v1alpha1.TopologyInfo{
		TopologyKey: v1alpha1.NetworkTopologyRegion,
		OriginList:  getOriginList(ctrl, nodes, manualRegionCosts, v1.LabelTopologyRegion),
	})

	topologyList = append(topologyList, v1alpha1.TopologyInfo{
		TopologyKey: v1alpha1.NetworkTopologyZone,
		OriginList:  getZoneList(ctrl, nodes, manualZoneCosts), // Only zones belonging to the same region
	})

	topologyList = append(topologyList, v1alpha1.TopologyInfo{
		TopologyKey: v1alpha1.NetworkTopologySegment,
		OriginList:  getOriginList(ctrl, nodes, manualSegmentCosts, string(v1alpha1.NetworkTopologySegment)),
	})
	return topologyList
}

func getOriginList(ctrl *NetworkTopologyController, nodes []*v1.Node, manualCosts v1alpha1.OriginList, label string) v1alpha1.OriginList {
	var list v1alpha1.OriginList
	var elements []string

	// Sort Costs by origin, might not be sorted since were manually defined
	sort.Sort(util.ByOrigin(manualCosts))

	for _, n := range nodes {
		r := util.GetNodeLabel(n, label)
		if !contains(elements, r) {
			elements = append(elements, r)
		}
	}

	klog.InfoS("[getOriginList] ", "Label", label, "elements", elements)

	for _, e1 := range elements {
		// init vars
		var costInfo []v1alpha1.CostInfo

		var costInterArray []int
		var costIntraArray []int

		// var bandwidthIntraArray []resource.Quantity
		var bandwidthInterArray []resource.Quantity

		for _, e2 := range elements {
			if e1 != e2 {
				var cost = 0
				if label == v1.LabelTopologyRegion {
					cost, _ = ctrl.regionGraph.GetPath(e1, e2)
				} else if label == string(v1alpha1.NetworkTopologySegment) {
					cost, _ = ctrl.segmentGraph.GetPath(e1, e2)
				}

				costInterArray = append(costInterArray, cost)
				klog.V(5).InfoS("[getOriginList]", "cost", int64(cost), "costInterarray", costInterArray)

				allocatable, ok := ctrl.BandwidthAllocatable[util.CostKey{ // Retrieve the current allocatable bandwidth from the map
					Origin:      e1,
					Destination: e2,
				}]

				originCosts := util.FindOriginCosts(manualCosts, e1)

				klog.V(5).InfoS("[getOriginList]", "originCosts", originCosts)

				bandwidthCapacity := *resource.NewScaledQuantity(1, resource.Giga)
				isAllowed := true

				if originCosts != nil {
					// Sort Costs by destination, might not be sorted since were manually defined
					sort.Sort(util.ByDestination(originCosts))

					bandwidthCapacity = util.FindOriginBandwidthCapacity(originCosts, e2)
					isAllowed = util.FindOriginIsAllowed(originCosts, e2)

					if bandwidthCapacity == resource.MustParse("0") {
						bandwidthCapacity = *resource.NewScaledQuantity(1, resource.Giga)
					}
				}

				klog.V(5).InfoS("[getOriginList]", "e1", e1, "e2", e2, "bandwidth_capacity", bandwidthCapacity.Value())

				bandwidthInterArray = append(bandwidthInterArray, bandwidthCapacity)

				if ok {
					info := v1alpha1.CostInfo{
						Destination:        e2,
						IsAllowed:          isAllowed,
						BandwidthCapacity:  bandwidthCapacity,
						BandwidthAllocated: allocatable,
						NetworkCost:        int64(cost),
					}
					klog.V(5).InfoS("[getOriginList]", "origin", e1, "Destination", e2, "Cost", info.NetworkCost, "Allocatable", info.BandwidthAllocated.Value())
					costInfo = append(costInfo, info)
				} else {
					info := v1alpha1.CostInfo{
						Destination:        e2,
						IsAllowed:          isAllowed,
						BandwidthCapacity:  bandwidthCapacity,
						BandwidthAllocated: *resource.NewQuantity(0, resource.DecimalSI), // consider as zero
						NetworkCost:        int64(cost),
					}
					klog.V(5).InfoS("[getOriginList]", "origin", e1, "Destination", e2, "Cost", info.NetworkCost, "Allocatable", info.BandwidthAllocated.Value())
					costInfo = append(costInfo, info)
				}
			}
		}

		// calculate origin intra statistics: costs between different nodes
		klog.V(5).InfoS("[getOriginList] ", "nodeGraph", ctrl.nodeGraph)
		for _, n1 := range nodes {
			e_n1 := util.GetNodeLabel(n1, label)
			for _, n2 := range nodes {
				e_n2 := util.GetNodeLabel(n2, label)
				if n1 != n2 {
					if e_n1 == e_n2 && e_n1 == e1 {
						klog.V(5).InfoS("[getZoneList] IntraStatistics", "n1", n1.Name, "n2", n2.Name, "element", e1)
						cost, _ := ctrl.nodeGraph.GetPath(n1.Name, n2.Name)
						costIntraArray = append(costIntraArray, cost)
					}
				}
			}
		}

		// Sort Costs by Destination
		sort.Sort(util.ByDestination(costInfo))

		minInter, avgInter, maxInter := util.GetMinAvgMax(costInterArray)
		minIntra, avgIntra, maxIntra := util.GetMinAvgMax(costIntraArray)
		minB, avgB, maxB := util.GetMinAvgMaxBandwidth(bandwidthInterArray)

		klog.InfoS("[getOriginList] InterStatistics - cost", "origin", e1, "costs", costInterArray, "min", minInter, "avg", avgInter, "max", maxInter)
		klog.InfoS("[getOriginList] IntraStatistics - cost", "origin", e1, "costs", costIntraArray, "min", minIntra, "avg", avgIntra, "max", maxIntra)
		klog.InfoS("[getOriginList] Inter/Intra Statistics - bandwidth", "origin", e1, "bandwidths", bandwidthInterArray, "min", minB, "avg", avgB, "max", maxB)

		originInfo := v1alpha1.OriginInfo{
			Origin: e1,
			OriginInterStatistics: v1alpha1.NetworkTopologyStatistics{
				MinBandwidth: minB,
				AvgBandwidth: avgB,
				MaxBandwidth: maxB,
				MinCost:      int64(minInter),
				AvgCost:      int64(avgInter),
				MaxCost:      int64(maxInter),
			},
			OriginIntraStatistics: v1alpha1.NetworkTopologyStatistics{
				MinBandwidth: minB,
				AvgBandwidth: avgB,
				MaxBandwidth: maxB,
				MinCost:      int64(minIntra),
				AvgCost:      int64(avgIntra),
				MaxCost:      int64(maxIntra),
			},
			CostList: costInfo,
		}
		list = append(list, originInfo)
	}

	// Sort regionList by origin
	sort.Sort(util.ByOrigin(list))
	return list
}

/* //Unused currently
func getRegionList(ctrl *NetworkTopologyController, nodes []*v1.Node, manualRegionCosts v1alpha1.OriginList) v1alpha1.OriginList {
	var regionList v1alpha1.OriginList
	var regions []string

	// Sort Costs by origin, might not be sorted since were manually defined
	sort.Sort(util.ByOrigin(manualRegionCosts))

	for _, n := range nodes {
		r := util.GetNodeLabel(n, v1.LabelTopologyRegion)
		if !contains(regions, r) {
			regions = append(regions, r)
		}
	}

	klog.V(5).InfoS("getRegionList:", "regions", regions)

	for _, r1 := range regions {
		// init vars
		var costInfo []v1alpha1.CostInfo
		var costArray []int

		for _, r2 := range regions {
			if r1 != r2 {
				cost, _ := ctrl.regionGraph.GetPath(r1, r2)
				costArray = append(costArray, cost)

				klog.V(5).InfoS("getRegionList: ", "cost", int64(cost), "costarray", costArray)

				allocatable, ok := ctrl.BandwidthAllocatable[util.CostKey{ // Retrieve the current allocatable bandwidth from the map (origin: zone, destination: pod zoneHostname)
					Origin:      r1,
					Destination: r2,
				}]

				originCosts := util.FindOriginCosts(manualRegionCosts, r1)

				klog.V(5).InfoS("getRegionList: ", "originCosts", originCosts)

				bandwidthCapacity := *resource.NewScaledQuantity(1, resource.Giga)
				isAllowed := true

				if originCosts != nil {
					// Sort Costs by destination, might not be sorted since were manually defined
					sort.Sort(util.ByDestination(originCosts))

					bandwidthCapacity = util.FindOriginBandwidthCapacity(originCosts, r2)
					isAllowed = util.FindOriginIsAllowed(originCosts, r2)

					if bandwidthCapacity == resource.MustParse("0") {
						bandwidthCapacity = *resource.NewScaledQuantity(1, resource.Giga)
					}
				}

				klog.V(5).InfoS("getRegionList:", "r1", r1, "r2", r2, "bandwidth_capacity", bandwidthCapacity.Value())

				if ok {
					info := v1alpha1.CostInfo{
						Destination:        r2,
						IsAllowed:          isAllowed,
						BandwidthCapacity:  bandwidthCapacity,
						BandwidthAllocated: allocatable,
						NetworkCost:        int64(cost),
					}
					klog.V(5).InfoS("getRegionList:", "origin", r1, "Destination", r2, "Cost", info.NetworkCost, "Allocatable", info.BandwidthAllocated.Value())
					costInfo = append(costInfo, info)
				} else {
					info := v1alpha1.CostInfo{
						Destination:        r2,
						IsAllowed:          isAllowed,
						BandwidthCapacity:  bandwidthCapacity,
						BandwidthAllocated: *resource.NewQuantity(0, resource.DecimalSI), // consider as zero
						NetworkCost:        int64(cost),
					}
					klog.V(5).InfoS("getRegionList:", "origin", r1, "Destination", r2, "Cost", info.NetworkCost, "Allocatable", info.BandwidthAllocated.Value())
					costInfo = append(costInfo, info)
				}
			}
		}

		// Sort Costs by Destination
		sort.Sort(util.ByDestination(costInfo))

		min, avg, max := util.GetMinAvgMax(costArray)
		klog.InfoS("[getRegionList] Get min, avg and max costs", "region", r1, "costs", costArray, "min", min, "avg", avg, "max", max)

		originInfo := v1alpha1.OriginInfo{
			Origin:   r1,
			MinCost:  int64(min),
			AvgCost:  int64(avg),
			MaxCost:  int64(max),
			CostList: costInfo,
		}
		regionList = append(regionList, originInfo)
	}

	// Sort regionList by origin
	sort.Sort(util.ByOrigin(regionList))
	return regionList
}
*/

func getZoneList(ctrl *NetworkTopologyController, nodes []*v1.Node, manualZoneCosts v1alpha1.OriginList) v1alpha1.OriginList {
	var zoneList v1alpha1.OriginList
	var zones []string

	for _, n := range nodes {
		z := util.GetNodeLabel(n, v1.LabelTopologyZone)
		if !contains(zones, z) {
			zones = append(zones, z)
		}
	}

	klog.Infof("[getZoneList] Zones %v ", zones)
	klog.V(5).Infof("[getZoneList] ZoneMap %v ", ctrl.ZoneMap)

	for _, z1 := range zones {
		// init vars
		var costInfo []v1alpha1.CostInfo

		// Inter
		var costInterArray []int
		var bandwidthInterArray []resource.Quantity

		// Intra
		var costIntraArray []int
		//var bandwidthIntraArray []resource.Quantity

		for _, z2 := range zones {
			if z1 != z2 {
				value, ok := ctrl.ZoneMap[util.ZoneKey{ // Check if zones belong to the same region
					Z1: z1,
					Z2: z2,
				}]

				if ok && value {
					cost, _ := ctrl.zoneGraph.GetPath(z1, z2)
					costInterArray = append(costInterArray, cost)

					allocatable, ok := ctrl.BandwidthAllocatable[util.CostKey{ // Retrieve the current allocatable bandwidth from the map (origin: zone, destination: pod zoneHostname)
						Origin:      z1,
						Destination: z2,
					}]

					originCosts := util.FindOriginCosts(manualZoneCosts, z1)

					klog.V(5).Infof("[getZoneList] originCosts: %v", originCosts)

					bandwidthCapacity := *resource.NewScaledQuantity(1, resource.Giga)
					isAllowed := true

					if originCosts != nil {
						// Sort Costs by destination, might not be sorted since were manually defined
						sort.Sort(util.ByDestination(originCosts))

						bandwidthCapacity = util.FindOriginBandwidthCapacity(originCosts, z2)
						isAllowed = util.FindOriginIsAllowed(originCosts, z2)

						if bandwidthCapacity == resource.MustParse("0") {
							bandwidthCapacity = *resource.NewScaledQuantity(1, resource.Giga)
						}
					}

					klog.V(5).Infof("[getZoneList] Bandwidth Capacity between %v and %v: %v", z1, z2, bandwidthCapacity)
					bandwidthInterArray = append(bandwidthInterArray, bandwidthCapacity)

					if ok {
						info := v1alpha1.CostInfo{
							Destination:        z2,
							IsAllowed:          isAllowed,
							BandwidthCapacity:  bandwidthCapacity,
							BandwidthAllocated: allocatable,
							NetworkCost:        int64(cost),
						}

						klog.V(5).Infof("[getZoneList] Origin %v - Destination %v - Cost: %v - Allocatable: %v", z1, z2, info.NetworkCost, info.BandwidthAllocated)
						costInfo = append(costInfo, info)

					} else {
						info := v1alpha1.CostInfo{
							Destination:        z2,
							IsAllowed:          isAllowed,
							BandwidthCapacity:  bandwidthCapacity,
							BandwidthAllocated: *resource.NewQuantity(0, resource.DecimalSI), // Consider as zero
							NetworkCost:        int64(cost),
						}

						klog.V(5).Infof("[getZoneList] Bandwidth Allocatable not found: Origin %v - Destination %v - Cost: %v - Allocatable: %v", z1, z2, info.NetworkCost, info.BandwidthAllocated)
						costInfo = append(costInfo, info)
					}
				}
			}
		}

		// calculate zone intra statistics: costs between different nodes
		klog.V(5).InfoS("[getZoneList] ", "nodeGraph", ctrl.nodeGraph)
		for _, n1 := range nodes {
			z_n1 := util.GetNodeLabel(n1, v1.LabelTopologyZone)
			for _, n2 := range nodes {
				z_n2 := util.GetNodeLabel(n2, v1.LabelTopologyZone)
				if n1 != n2 {
					if z_n1 == z_n2 && z_n1 == z1 {
						klog.V(5).InfoS("[getZoneList] IntraStatistics", "n1", n1.Name, "n2", n2.Name, "zone", z1)
						cost, _ := ctrl.nodeGraph.GetPath(n1.Name, n2.Name)
						costIntraArray = append(costIntraArray, cost)
					}
				}
			}
		}

		// Sort Costs by Destination -> new
		sort.Sort(util.ByDestination(costInfo))

		minInter, avgInter, maxInter := util.GetMinAvgMax(costInterArray)
		minIntra, avgIntra, maxIntra := util.GetMinAvgMax(costIntraArray)
		minB, avgB, maxB := util.GetMinAvgMaxBandwidth(bandwidthInterArray)

		klog.InfoS("[getZoneList] InterStatistics - cost", "zone", z1, "costs", costInterArray, "min", minInter, "avg", avgInter, "max", maxInter)
		klog.InfoS("[getZoneList] IntraStatistics - cost", "zone", z1, "costs", costIntraArray, "min", minIntra, "avg", avgIntra, "max", maxIntra)
		klog.InfoS("[getZoneList] Inter/Intra Statistics - bandwidth", "zone", z1, "bandwidths", bandwidthInterArray, "min", minB, "avg", avgB, "max", maxB)

		originInfo := v1alpha1.OriginInfo{
			Origin: z1,
			OriginInterStatistics: v1alpha1.NetworkTopologyStatistics{
				MinBandwidth: minB,
				AvgBandwidth: avgB,
				MaxBandwidth: maxB,
				MinCost:      int64(minInter),
				AvgCost:      int64(avgInter),
				MaxCost:      int64(maxInter),
			},
			OriginIntraStatistics: v1alpha1.NetworkTopologyStatistics{
				MinBandwidth: minB,
				AvgBandwidth: avgB,
				MaxBandwidth: maxB,
				MinCost:      int64(minIntra),
				AvgCost:      int64(avgIntra),
				MaxCost:      int64(maxIntra),
			},
			CostList: costInfo,
		}
		zoneList = append(zoneList, originInfo)
	}

	// Sort Costs by origin
	sort.Sort(util.ByOrigin(zoneList))
	return zoneList
}

func getSegmentList(ctrl *NetworkTopologyController, nodes []*v1.Node, manualSegmentCosts v1alpha1.OriginList) v1alpha1.OriginList {
	var segmentList v1alpha1.OriginList
	var segments []string

	for _, n := range nodes {
		s := util.GetNodeLabel(n, string(v1alpha1.NetworkTopologySegment))
		if !contains(segments, s) {
			segments = append(segments, s)
		}
	}

	klog.Infof("[getSegmentList] Segments %v ", segments)
	klog.Infof("[getSegmentList] SegmentMap %v ", ctrl.SegmentMap)

	for _, s1 := range segments {
		// init vars
		var costInfo []v1alpha1.CostInfo
		var costArray []int
		var bandwidthArray []resource.Quantity

		for _, s2 := range segments {
			if s1 != s2 {
				value, ok := ctrl.SegmentMap[util.SegmentKey{ // Check if segments belong to the same region
					S1: s1,
					S2: s2,
				}]

				if ok && value {
					cost, _ := ctrl.segmentGraph.GetPath(s1, s2)
					costArray = append(costArray, cost)

					allocatable, ok := ctrl.BandwidthAllocatable[util.CostKey{ // Retrieve the current allocatable bandwidth from the map (origin: zone, destination: pod zoneHostname)
						Origin:      s1,
						Destination: s2,
					}]

					originCosts := util.FindOriginCosts(manualSegmentCosts, s1)
					klog.V(5).Infof("[getSegmentList] originCosts: %v", originCosts)

					bandwidthCapacity := *resource.NewScaledQuantity(1, resource.Giga)
					isAllowed := true

					if originCosts != nil {
						// Sort Costs by destination, might not be sorted since were manually defined
						sort.Sort(util.ByDestination(originCosts))

						bandwidthCapacity = util.FindOriginBandwidthCapacity(originCosts, s2)
						isAllowed = util.FindOriginIsAllowed(originCosts, s2)

						if bandwidthCapacity == resource.MustParse("0") {
							bandwidthCapacity = *resource.NewScaledQuantity(1, resource.Giga)
						}
					}

					klog.V(5).Infof("[getSegmentList] Bandwidth Capacity between %v and %v: %v", s1, s2, bandwidthCapacity)
					bandwidthArray = append(bandwidthArray, bandwidthCapacity)

					if ok {
						info := v1alpha1.CostInfo{
							Destination:        s2,
							IsAllowed:          isAllowed,
							BandwidthCapacity:  bandwidthCapacity,
							BandwidthAllocated: allocatable,
							NetworkCost:        int64(cost),
						}

						klog.V(5).Infof("[getSegmentList] Origin %v - Destination %v - isAllowed %v - Cost: %v - Allocatable: %v", s1, s2,
							info.IsAllowed, info.NetworkCost, info.BandwidthAllocated)
						costInfo = append(costInfo, info)
					} else {
						info := v1alpha1.CostInfo{
							Destination:        s2,
							IsAllowed:          isAllowed,
							BandwidthCapacity:  bandwidthCapacity,
							BandwidthAllocated: *resource.NewQuantity(0, resource.DecimalSI), // Consider as zero
							NetworkCost:        int64(cost),
						}

						klog.V(5).Infof("[getSegmentList] Bandwidth Allocatable not found: Origin %v - Destination %v "+
							"isAllowed %v - Cost: %v - Allocatable: %v", s1, s2, info.IsAllowed, info.NetworkCost, info.BandwidthAllocated)
						costInfo = append(costInfo, info)
					}
				}
			}
		}

		// Sort Costs by Destination -> new
		sort.Sort(util.ByDestination(costInfo))

		min, avg, max := util.GetMinAvgMax(costArray)
		minB, avgB, maxB := util.GetMinAvgMaxBandwidth(bandwidthArray)

		klog.InfoS("[getSegmentList] Get min, avg and max costs", "segment", s1, "costs", costArray, "min", min, "avg", avg, "max", max)
		klog.InfoS("[getSegmentList] Get min, avg and max bandwidths", "segment", s1, "bandwidths", bandwidthArray, "min", minB, "avg", avgB, "max", maxB)

		originInfo := v1alpha1.OriginInfo{
			Origin: s1,
			OriginInterStatistics: v1alpha1.NetworkTopologyStatistics{
				MinBandwidth: minB,
				AvgBandwidth: avgB,
				MaxBandwidth: maxB,
				MinCost:      int64(min),
				AvgCost:      int64(avg),
				MaxCost:      int64(max),
			},
			OriginIntraStatistics: v1alpha1.NetworkTopologyStatistics{
				MinBandwidth: minB,
				AvgBandwidth: avgB,
				MaxBandwidth: maxB,
				MinCost:      int64(min),
				AvgCost:      int64(avg),
				MaxCost:      int64(max),
			},
			CostList: costInfo,
		}
		segmentList = append(segmentList, originInfo)
	}

	// Sort Costs by origin
	sort.Sort(util.ByOrigin(segmentList))
	return segmentList
}

// contains checks if a string is present in a slice
func contains(s []string, str string) bool {
	for _, value := range s {
		if value == str {
			return true
		}
	}
	return false
}
