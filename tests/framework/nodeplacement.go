package framework

import (
	"context"
	"fmt"
	"reflect"
	"runtime"

	sdkapi "kubevirt.io/controller-lifecycle-operator-sdk/api"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	nodeSelectorTestValue = map[string]string{"kubernetes.io/arch": runtime.GOARCH}
	tolerationsTestValue  = []v1.Toleration{{Key: "test", Value: "123"}, {Key: "CriticalAddonsOnly", Value: string(v1.TolerationOpExists)}}
	affinityTestValue     = &v1.Affinity{}
)

// TestNodePlacementValues returns a pre-defined set of node placement values for testing purposes.
// The values chosen are valid, but the pod will likely not be schedulable.
func (f *Framework) TestNodePlacementValues() sdkapi.NodePlacement {
	nodes, _ := f.K8sClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})

	nodeName := nodes.Items[0].Name
	for _, node := range nodes.Items {
		if _, hasLabel := node.Labels["node-role.kubernetes.io/worker"]; hasLabel {
			nodeName = node.Name
			break
		}
	}

	affinityTestValue = &v1.Affinity{
		NodeAffinity: &v1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
				NodeSelectorTerms: []v1.NodeSelectorTerm{
					{
						MatchExpressions: []v1.NodeSelectorRequirement{
							{Key: "kubernetes.io/hostname", Operator: v1.NodeSelectorOpIn, Values: []string{nodeName}},
						},
					},
				},
			},
		},
	}

	return sdkapi.NodePlacement{
		NodeSelector: nodeSelectorTestValue, //here
		Affinity:     affinityTestValue,     //here
		Tolerations:  tolerationsTestValue,
	}
}

// PodSpecHasTestNodePlacementValues compares if the pod spec has the set of node placement values defined for testing purposes
func (f *Framework) PodSpecHasTestNodePlacementValues(podSpec v1.PodSpec) error {
	errfmt := "mismatched nodeSelectors, podSpec:\n%v\nExpected:\n%v\n"
	if !reflect.DeepEqual(podSpec.NodeSelector, nodeSelectorTestValue) || !reflect.DeepEqual(podSpec.Affinity, affinityTestValue) {
		return fmt.Errorf(errfmt, podSpec.NodeSelector, nodeSelectorTestValue)
	}
	foundMatchingToleration := false
	for _, toleration := range podSpec.Tolerations {
		if toleration == tolerationsTestValue[0] {
			foundMatchingToleration = true
		}
	}
	if !foundMatchingToleration {
		return fmt.Errorf(errfmt, podSpec.NodeSelector, nodeSelectorTestValue)
	}
	return nil
}
