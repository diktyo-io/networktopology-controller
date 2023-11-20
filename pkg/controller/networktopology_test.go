package controller

import (
	"context"
	"fmt"
	agfake "github.com/diktyo-io/appgroup-api/pkg/generated/clientset/versioned/fake"
	agInformer "github.com/diktyo-io/appgroup-api/pkg/generated/informers/externalversions"
	"github.com/diktyo-io/networktopology-api/pkg/apis/networktopology/v1alpha1"
	ntfake "github.com/diktyo-io/networktopology-api/pkg/generated/clientset/versioned/fake"
	ntInformer "github.com/diktyo-io/networktopology-api/pkg/generated/informers/externalversions"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/controller"
	st "k8s.io/kubernetes/pkg/scheduler/testing"
	"testing"
	"time"
)

func TestNetworkTopologyController_Run(t *testing.T) {
	ctx := context.TODO()
	//createTime := metav1.Time{Time: time.Now().Add(-72 * time.Hour)}

	// Create Network Topology CRD
	networkTopology := &v1alpha1.NetworkTopology{
		ObjectMeta: metav1.ObjectMeta{Name: "nt-test", Namespace: "default"},
		Spec: v1alpha1.NetworkTopologySpec{
			ConfigmapName: "netperfMetrics",
			Weights: v1alpha1.WeightList{
				v1alpha1.WeightInfo{Name: "UserDefined",
					TopologyList: v1alpha1.TopologyList{
						v1alpha1.TopologyInfo{
							TopologyKey: "topology.kubernetes.io/region",
							OriginList: v1alpha1.OriginList{
								v1alpha1.OriginInfo{
									Origin: "us-west-1",
									CostList: []v1alpha1.CostInfo{{
										Destination:       "us-west-2",
										BandwidthCapacity: *resource.NewScaledQuantity(1, resource.Giga),
										NetworkCost:       20},
									},
								},
								v1alpha1.OriginInfo{
									Origin: "us-west-2",
									CostList: []v1alpha1.CostInfo{{
										Destination:       "us-west-1",
										BandwidthCapacity: *resource.NewScaledQuantity(1, resource.Giga),
										NetworkCost:       30},
									},
								},
							}},
						v1alpha1.TopologyInfo{
							TopologyKey: "topology.kubernetes.io/zone",
							OriginList: v1alpha1.OriginList{
								v1alpha1.OriginInfo{
									Origin: "Z1",
									CostList: []v1alpha1.CostInfo{{
										Destination:       "Z2",
										BandwidthCapacity: *resource.NewScaledQuantity(500, resource.Mega),
										NetworkCost:       7},
									},
								},
								v1alpha1.OriginInfo{
									Origin: "Z2",
									CostList: []v1alpha1.CostInfo{{
										Destination:       "Z1",
										BandwidthCapacity: *resource.NewScaledQuantity(500, resource.Mega),
										NetworkCost:       5},
									},
								},
								v1alpha1.OriginInfo{
									Origin: "Z3",
									CostList: []v1alpha1.CostInfo{
										{
											Destination:       "Z4",
											BandwidthCapacity: *resource.NewScaledQuantity(500, resource.Mega),
											NetworkCost:       10},
										{
											Destination:       "Z5",
											BandwidthCapacity: *resource.NewScaledQuantity(600, resource.Mega),
											NetworkCost:       15},
									},
								},
								v1alpha1.OriginInfo{
									Origin: "Z4",
									CostList: []v1alpha1.CostInfo{
										{
											Destination:       "Z3",
											BandwidthCapacity: *resource.NewScaledQuantity(500, resource.Mega),
											NetworkCost:       10},
										{
											Destination:       "Z5",
											BandwidthCapacity: *resource.NewScaledQuantity(700, resource.Mega),
											NetworkCost:       15},
									},
								},
								v1alpha1.OriginInfo{
									Origin: "Z5",
									CostList: []v1alpha1.CostInfo{
										{
											Destination:       "Z3",
											BandwidthCapacity: *resource.NewScaledQuantity(500, resource.Mega),
											NetworkCost:       10},
										{
											Destination:       "Z4",
											BandwidthCapacity: *resource.NewScaledQuantity(700, resource.Mega),
											NetworkCost:       15},
									},
								},
							},
						},
						v1alpha1.TopologyInfo{
							TopologyKey: "topology.kubernetes.io/segment",
							OriginList: v1alpha1.OriginList{
								v1alpha1.OriginInfo{
									Origin: "S1",
									CostList: []v1alpha1.CostInfo{
										{
											Destination:       "S2",
											BandwidthCapacity: *resource.NewScaledQuantity(500, resource.Mega),
											NetworkCost:       10},
										{
											Destination:       "S3",
											BandwidthCapacity: *resource.NewScaledQuantity(700, resource.Mega),
											NetworkCost:       15},
									},
								},
								v1alpha1.OriginInfo{
									Origin: "S2",
									CostList: []v1alpha1.CostInfo{
										{
											Destination:       "S1",
											BandwidthCapacity: *resource.NewScaledQuantity(500, resource.Mega),
											NetworkCost:       10},
										{
											Destination:       "S3",
											BandwidthCapacity: *resource.NewScaledQuantity(700, resource.Mega),
											NetworkCost:       15},
									},
								},
								v1alpha1.OriginInfo{
									Origin: "S3",
									CostList: []v1alpha1.CostInfo{
										{
											Destination:       "S1",
											BandwidthCapacity: *resource.NewScaledQuantity(500, resource.Mega),
											NetworkCost:       10},
										{
											Destination:       "S2",
											BandwidthCapacity: *resource.NewScaledQuantity(700, resource.Mega),
											NetworkCost:       15},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Create Nodes
	nodes := []*v1.Node{
		st.MakeNode().Name("n1").Label(v1.LabelTopologyRegion, "us-west-1").Label(v1.LabelTopologyZone, "Z1").
			Label(string(v1alpha1.NetworkTopologySegment), "S1").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n2").Label(v1.LabelTopologyRegion, "us-west-1").Label(v1.LabelTopologyZone, "Z1").
			Label(string(v1alpha1.NetworkTopologySegment), "S2").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n3").Label(v1.LabelTopologyRegion, "us-west-1").Label(v1.LabelTopologyZone, "Z2").
			Label(string(v1alpha1.NetworkTopologySegment), "S3").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n4").Label(v1.LabelTopologyRegion, "us-west-1").Label(v1.LabelTopologyZone, "Z2").
			Label(string(v1alpha1.NetworkTopologySegment), "S1").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n5").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z3").
			Label(string(v1alpha1.NetworkTopologySegment), "S2").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n6").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z3").
			Label(string(v1alpha1.NetworkTopologySegment), "S3").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n7").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z3").
			Label(string(v1alpha1.NetworkTopologySegment), "S1").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n8").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z4").
			Label(string(v1alpha1.NetworkTopologySegment), "S2").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n9").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z4").
			Label(string(v1alpha1.NetworkTopologySegment), "S3").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n10").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z4").
			Label(string(v1alpha1.NetworkTopologySegment), "S1").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n11").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z5").
			Label(string(v1alpha1.NetworkTopologySegment), "S2").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n12").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z5").
			Label(string(v1alpha1.NetworkTopologySegment), "S3").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n13").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z5").
			Label(string(v1alpha1.NetworkTopologySegment), "S1").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		/*st.MakeNode().Name("n14").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z5").
			Label(string(v1alpha1.NetworkTopologySegment), "S5").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n15").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z5").
			Label(string(v1alpha1.NetworkTopologySegment), "S1").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n16").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z6").
			Label(string(v1alpha1.NetworkTopologySegment), "S2").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n17").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z6").
			Label(string(v1alpha1.NetworkTopologySegment), "S3").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n18").Label(v1.LabelTopologyRegion, "us-west-2").Label(v1.LabelTopologyZone, "Z6").
			Label(string(v1alpha1.NetworkTopologySegment), "S4").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n19").Label(v1.LabelTopologyRegion, "us-west-3").Label(v1.LabelTopologyZone, "Z7").
			Label(string(v1alpha1.NetworkTopologySegment), "S1").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n20").Label(v1.LabelTopologyRegion, "us-west-3").Label(v1.LabelTopologyZone, "Z7").
			Label(string(v1alpha1.NetworkTopologySegment), "S2").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n21").Label(v1.LabelTopologyRegion, "us-west-3").Label(v1.LabelTopologyZone, "Z7").
			Label(string(v1alpha1.NetworkTopologySegment), "S3").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n22").Label(v1.LabelTopologyRegion, "us-west-3").Label(v1.LabelTopologyZone, "Z8").
			Label(string(v1alpha1.NetworkTopologySegment), "S4").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n23").Label(v1.LabelTopologyRegion, "us-west-3").Label(v1.LabelTopologyZone, "Z8").
			Label(string(v1alpha1.NetworkTopologySegment), "S5").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n24").Label(v1.LabelTopologyRegion, "us-west-3").Label(v1.LabelTopologyZone, "Z8").
			Label(string(v1alpha1.NetworkTopologySegment), "S1").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n25").Label(v1.LabelTopologyRegion, "us-west-3").Label(v1.LabelTopologyZone, "Z9").
			Label(string(v1alpha1.NetworkTopologySegment), "S2").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n26").Label(v1.LabelTopologyRegion, "us-west-3").Label(v1.LabelTopologyZone, "Z9").
			Label(string(v1alpha1.NetworkTopologySegment), "S3").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(),
		st.MakeNode().Name("n27").Label(v1.LabelTopologyRegion, "us-west-3").Label(v1.LabelTopologyZone, "Z9").
			Label(string(v1alpha1.NetworkTopologySegment), "S4").Capacity(
			map[v1.ResourceName]string{v1.ResourceCPU: "8000m", v1.ResourceMemory: "16Gi"}).Obj(), */
	}

	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "netperfMetrics", Namespace: "default"},
		Data: map[string]string{ // netperf.p90.latency.milliseconds.origin.%s.destination.%s
			"netperf.p90.latency.milliseconds.origin.n1.destination.n2":   "4",
			"netperf.p90.latency.milliseconds.origin.n1.destination.n3":   "5",
			"netperf.p90.latency.milliseconds.origin.n1.destination.n4":   "5",
			"netperf.p90.latency.milliseconds.origin.n1.destination.n5":   "20",
			"netperf.p90.latency.milliseconds.origin.n1.destination.n6":   "30",
			"netperf.p90.latency.milliseconds.origin.n1.destination.n7":   "400",
			"netperf.p90.latency.milliseconds.origin.n1.destination.n8":   "50",
			"netperf.p90.latency.milliseconds.origin.n1.destination.n9":   "20",
			"netperf.p90.latency.milliseconds.origin.n1.destination.n10":  "30",
			"netperf.p90.latency.milliseconds.origin.n1.destination.n11":  "400",
			"netperf.p90.latency.milliseconds.origin.n1.destination.n12":  "50",
			"netperf.p90.latency.milliseconds.origin.n1.destination.n13":  "50",
			"netperf.p90.latency.milliseconds.origin.n2.destination.n1":   "2",
			"netperf.p90.latency.milliseconds.origin.n2.destination.n3":   "5",
			"netperf.p90.latency.milliseconds.origin.n2.destination.n4":   "5",
			"netperf.p90.latency.milliseconds.origin.n2.destination.n5":   "20",
			"netperf.p90.latency.milliseconds.origin.n2.destination.n6":   "100",
			"netperf.p90.latency.milliseconds.origin.n2.destination.n7":   "30",
			"netperf.p90.latency.milliseconds.origin.n2.destination.n8":   "50",
			"netperf.p90.latency.milliseconds.origin.n2.destination.n9":   "20",
			"netperf.p90.latency.milliseconds.origin.n2.destination.n10":  "30",
			"netperf.p90.latency.milliseconds.origin.n2.destination.n11":  "400",
			"netperf.p90.latency.milliseconds.origin.n2.destination.n12":  "50",
			"netperf.p90.latency.milliseconds.origin.n2.destination.n13":  "50",
			"netperf.p90.latency.milliseconds.origin.n3.destination.n1":   "5",
			"netperf.p90.latency.milliseconds.origin.n3.destination.n2":   "5",
			"netperf.p90.latency.milliseconds.origin.n3.destination.n4":   "50",
			"netperf.p90.latency.milliseconds.origin.n3.destination.n5":   "10",
			"netperf.p90.latency.milliseconds.origin.n3.destination.n6":   "20",
			"netperf.p90.latency.milliseconds.origin.n3.destination.n7":   "100",
			"netperf.p90.latency.milliseconds.origin.n3.destination.n8":   "10",
			"netperf.p90.latency.milliseconds.origin.n3.destination.n9":   "20",
			"netperf.p90.latency.milliseconds.origin.n3.destination.n10":  "30",
			"netperf.p90.latency.milliseconds.origin.n3.destination.n11":  "400",
			"netperf.p90.latency.milliseconds.origin.n3.destination.n12":  "50",
			"netperf.p90.latency.milliseconds.origin.n3.destination.n13":  "50",
			"netperf.p90.latency.milliseconds.origin.n4.destination.n1":   "57",
			"netperf.p90.latency.milliseconds.origin.n4.destination.n2":   "2",
			"netperf.p90.latency.milliseconds.origin.n4.destination.n3":   "4",
			"netperf.p90.latency.milliseconds.origin.n4.destination.n5":   "8",
			"netperf.p90.latency.milliseconds.origin.n4.destination.n6":   "12",
			"netperf.p90.latency.milliseconds.origin.n4.destination.n7":   "12",
			"netperf.p90.latency.milliseconds.origin.n4.destination.n8":   "43",
			"netperf.p90.latency.milliseconds.origin.n4.destination.n9":   "20",
			"netperf.p90.latency.milliseconds.origin.n4.destination.n10":  "30",
			"netperf.p90.latency.milliseconds.origin.n4.destination.n11":  "400",
			"netperf.p90.latency.milliseconds.origin.n4.destination.n12":  "50",
			"netperf.p90.latency.milliseconds.origin.n4.destination.n13":  "50",
			"netperf.p90.latency.milliseconds.origin.n5.destination.n1":   "30",
			"netperf.p90.latency.milliseconds.origin.n5.destination.n2":   "60",
			"netperf.p90.latency.milliseconds.origin.n5.destination.n3":   "30",
			"netperf.p90.latency.milliseconds.origin.n5.destination.n4":   "80",
			"netperf.p90.latency.milliseconds.origin.n5.destination.n6":   "1",
			"netperf.p90.latency.milliseconds.origin.n5.destination.n7":   "5",
			"netperf.p90.latency.milliseconds.origin.n5.destination.n8":   "5",
			"netperf.p90.latency.milliseconds.origin.n5.destination.n9":   "20",
			"netperf.p90.latency.milliseconds.origin.n5.destination.n10":  "30",
			"netperf.p90.latency.milliseconds.origin.n5.destination.n11":  "400",
			"netperf.p90.latency.milliseconds.origin.n5.destination.n12":  "50",
			"netperf.p90.latency.milliseconds.origin.n5.destination.n13":  "50",
			"netperf.p90.latency.milliseconds.origin.n6.destination.n1":   "10",
			"netperf.p90.latency.milliseconds.origin.n6.destination.n2":   "50",
			"netperf.p90.latency.milliseconds.origin.n6.destination.n3":   "10",
			"netperf.p90.latency.milliseconds.origin.n6.destination.n4":   "20",
			"netperf.p90.latency.milliseconds.origin.n6.destination.n5":   "3",
			"netperf.p90.latency.milliseconds.origin.n6.destination.n7":   "5",
			"netperf.p90.latency.milliseconds.origin.n6.destination.n8":   "5",
			"netperf.p90.latency.milliseconds.origin.n6.destination.n9":   "20",
			"netperf.p90.latency.milliseconds.origin.n6.destination.n10":  "30",
			"netperf.p90.latency.milliseconds.origin.n6.destination.n11":  "400",
			"netperf.p90.latency.milliseconds.origin.n6.destination.n12":  "50",
			"netperf.p90.latency.milliseconds.origin.n6.destination.n13":  "50",
			"netperf.p90.latency.milliseconds.origin.n7.destination.n1":   "10",
			"netperf.p90.latency.milliseconds.origin.n7.destination.n2":   "50",
			"netperf.p90.latency.milliseconds.origin.n7.destination.n3":   "10",
			"netperf.p90.latency.milliseconds.origin.n7.destination.n4":   "67",
			"netperf.p90.latency.milliseconds.origin.n7.destination.n5":   "5",
			"netperf.p90.latency.milliseconds.origin.n7.destination.n6":   "5",
			"netperf.p90.latency.milliseconds.origin.n7.destination.n8":   "2",
			"netperf.p90.latency.milliseconds.origin.n7.destination.n9":   "20",
			"netperf.p90.latency.milliseconds.origin.n7.destination.n10":  "30",
			"netperf.p90.latency.milliseconds.origin.n7.destination.n11":  "400",
			"netperf.p90.latency.milliseconds.origin.n7.destination.n12":  "50",
			"netperf.p90.latency.milliseconds.origin.n7.destination.n13":  "50",
			"netperf.p90.latency.milliseconds.origin.n8.destination.n1":   "70",
			"netperf.p90.latency.milliseconds.origin.n8.destination.n2":   "10",
			"netperf.p90.latency.milliseconds.origin.n8.destination.n3":   "40",
			"netperf.p90.latency.milliseconds.origin.n8.destination.n4":   "10",
			"netperf.p90.latency.milliseconds.origin.n8.destination.n5":   "5",
			"netperf.p90.latency.milliseconds.origin.n8.destination.n6":   "5",
			"netperf.p90.latency.milliseconds.origin.n8.destination.n7":   "3",
			"netperf.p90.latency.milliseconds.origin.n8.destination.n9":   "20",
			"netperf.p90.latency.milliseconds.origin.n8.destination.n10":  "30",
			"netperf.p90.latency.milliseconds.origin.n8.destination.n11":  "400",
			"netperf.p90.latency.milliseconds.origin.n8.destination.n12":  "50",
			"netperf.p90.latency.milliseconds.origin.n8.destination.n13":  "50",
			"netperf.p90.latency.milliseconds.origin.n9.destination.n1":   "70",
			"netperf.p90.latency.milliseconds.origin.n9.destination.n2":   "10",
			"netperf.p90.latency.milliseconds.origin.n9.destination.n3":   "40",
			"netperf.p90.latency.milliseconds.origin.n9.destination.n4":   "10",
			"netperf.p90.latency.milliseconds.origin.n9.destination.n5":   "5",
			"netperf.p90.latency.milliseconds.origin.n9.destination.n6":   "5",
			"netperf.p90.latency.milliseconds.origin.n9.destination.n7":   "3",
			"netperf.p90.latency.milliseconds.origin.n9.destination.n8":   "20",
			"netperf.p90.latency.milliseconds.origin.n9.destination.n10":  "30",
			"netperf.p90.latency.milliseconds.origin.n9.destination.n11":  "400",
			"netperf.p90.latency.milliseconds.origin.n9.destination.n12":  "50",
			"netperf.p90.latency.milliseconds.origin.n9.destination.n13":  "50",
			"netperf.p90.latency.milliseconds.origin.n10.destination.n1":  "70",
			"netperf.p90.latency.milliseconds.origin.n10.destination.n2":  "10",
			"netperf.p90.latency.milliseconds.origin.n10.destination.n3":  "40",
			"netperf.p90.latency.milliseconds.origin.n10.destination.n4":  "10",
			"netperf.p90.latency.milliseconds.origin.n10.destination.n5":  "5",
			"netperf.p90.latency.milliseconds.origin.n10.destination.n6":  "5",
			"netperf.p90.latency.milliseconds.origin.n10.destination.n7":  "3",
			"netperf.p90.latency.milliseconds.origin.n10.destination.n8":  "20",
			"netperf.p90.latency.milliseconds.origin.n10.destination.n9":  "30",
			"netperf.p90.latency.milliseconds.origin.n10.destination.n11": "400",
			"netperf.p90.latency.milliseconds.origin.n10.destination.n12": "50",
			"netperf.p90.latency.milliseconds.origin.n10.destination.n13": "50",
			"netperf.p90.latency.milliseconds.origin.n11.destination.n1":  "70",
			"netperf.p90.latency.milliseconds.origin.n11.destination.n2":  "10",
			"netperf.p90.latency.milliseconds.origin.n11.destination.n3":  "40",
			"netperf.p90.latency.milliseconds.origin.n11.destination.n4":  "10",
			"netperf.p90.latency.milliseconds.origin.n11.destination.n5":  "5",
			"netperf.p90.latency.milliseconds.origin.n11.destination.n6":  "5",
			"netperf.p90.latency.milliseconds.origin.n11.destination.n7":  "3",
			"netperf.p90.latency.milliseconds.origin.n11.destination.n8":  "20",
			"netperf.p90.latency.milliseconds.origin.n11.destination.n9":  "30",
			"netperf.p90.latency.milliseconds.origin.n11.destination.n10": "400",
			"netperf.p90.latency.milliseconds.origin.n11.destination.n12": "50",
			"netperf.p90.latency.milliseconds.origin.n11.destination.n13": "50",
			"netperf.p90.latency.milliseconds.origin.n12.destination.n1":  "70",
			"netperf.p90.latency.milliseconds.origin.n12.destination.n2":  "10",
			"netperf.p90.latency.milliseconds.origin.n12.destination.n3":  "40",
			"netperf.p90.latency.milliseconds.origin.n12.destination.n4":  "10",
			"netperf.p90.latency.milliseconds.origin.n12.destination.n5":  "5",
			"netperf.p90.latency.milliseconds.origin.n12.destination.n6":  "5",
			"netperf.p90.latency.milliseconds.origin.n12.destination.n7":  "3",
			"netperf.p90.latency.milliseconds.origin.n12.destination.n8":  "20",
			"netperf.p90.latency.milliseconds.origin.n12.destination.n9":  "30",
			"netperf.p90.latency.milliseconds.origin.n12.destination.n10": "400",
			"netperf.p90.latency.milliseconds.origin.n12.destination.n11": "50",
			"netperf.p90.latency.milliseconds.origin.n12.destination.n13": "50",
			"netperf.p90.latency.milliseconds.origin.n13.destination.n1":  "70",
			"netperf.p90.latency.milliseconds.origin.n13.destination.n2":  "10",
			"netperf.p90.latency.milliseconds.origin.n13.destination.n3":  "40",
			"netperf.p90.latency.milliseconds.origin.n13.destination.n4":  "10",
			"netperf.p90.latency.milliseconds.origin.n13.destination.n5":  "5",
			"netperf.p90.latency.milliseconds.origin.n13.destination.n6":  "5",
			"netperf.p90.latency.milliseconds.origin.n13.destination.n7":  "3",
			"netperf.p90.latency.milliseconds.origin.n13.destination.n8":  "20",
			"netperf.p90.latency.milliseconds.origin.n13.destination.n9":  "30",
			"netperf.p90.latency.milliseconds.origin.n13.destination.n10": "400",
			"netperf.p90.latency.milliseconds.origin.n13.destination.n11": "50",
			"netperf.p90.latency.milliseconds.origin.n13.destination.n12": "50",
		},
		BinaryData: nil,
	}

	cases := []struct {
		name                      string
		ntName                    string
		networkTopology           *v1alpha1.NetworkTopology
		nodes                     []*v1.Node
		networkTopologyCreateTime *metav1.Time
		configMap                 *v1.ConfigMap
		desiredNodeCount          int64
	}{
		{
			name:             "Network Topology controller: 13 nodes",
			ntName:           "nt-test",
			networkTopology:  networkTopology,
			nodes:            nodes,
			configMap:        configMap,
			desiredNodeCount: 13,
		}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {

			var kubeClient = fake.NewSimpleClientset()

			//print(len(nodes))

			if len(nodes) == 13 {
				kubeClient = fake.NewSimpleClientset(nodes[0], nodes[1], nodes[2], nodes[3], nodes[4], nodes[5], nodes[6], nodes[7],
					nodes[8], nodes[9], nodes[10], nodes[11], nodes[12],
					// nodes[13], nodes[14], nodes[15], nodes[16], nodes[17],
					//nodes[18], nodes[19], nodes[20], nodes[21], nodes[22], nodes[23], nodes[24], nodes[25], nodes[26],
					configMap)
			}

			ntClient := ntfake.NewSimpleClientset(networkTopology)
			agClient := agfake.NewSimpleClientset()

			informerFactory := informers.NewSharedInformerFactory(kubeClient, controller.NoResyncPeriodFunc())
			ntInformerFactory := ntInformer.NewSharedInformerFactory(ntClient, controller.NoResyncPeriodFunc())
			agInformerFactory := agInformer.NewSharedInformerFactory(agClient, controller.NoResyncPeriodFunc())

			ntInfo := ntInformerFactory.Networktopology().V1alpha1().NetworkTopologies()
			agInfo := agInformerFactory.Appgroup().V1alpha1().AppGroups()

			nodeInformer := informerFactory.Core().V1().Nodes()
			podInformer := informerFactory.Core().V1().Pods()
			configmapInformer := informerFactory.Core().V1().ConfigMaps()

			ctrl := NewNetworkTopologyController(kubeClient, ntInfo, agInfo, nodeInformer, podInformer, configmapInformer, ntClient)

			ntInformerFactory.Start(ctx.Done())
			informerFactory.Start(ctx.Done())

			go ctrl.Run(1, ctx.Done())
			err := wait.Poll(200*time.Millisecond, 1*time.Second, func() (done bool, err error) {
				nt, err := ntClient.NetworktopologyV1alpha1().NetworkTopologies("default").Get(ctx, c.ntName, metav1.GetOptions{})
				if err != nil {
					return false, err
				}
				if nt.Status.NodeCount != c.desiredNodeCount {
					return false, fmt.Errorf("want %v, got %v", c.desiredNodeCount, nt.Status.NodeCount)
				}
				//if nt.Spec.Weights == nil {
				//	return false, fmt.Errorf("want %v, got %v", c.desiredWeights, nt.Status.Weights)
				//}

				//for _, w := range nt.Status.Weights {
				//	for _, cost := range w.CostList {
				//	}
				//}
				return true, nil
			})
			if err != nil {
				t.Fatal("Unexpected error", err)
			}
		})
	}
}
