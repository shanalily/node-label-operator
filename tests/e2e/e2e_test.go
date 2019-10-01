// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT license.

package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Azure/go-autorest/autorest/to"
	"github.com/Azure/node-label-operator/azure"
	"github.com/Azure/node-label-operator/controller"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func Test(t *testing.T) {
	c := &Cluster{}
	c.KubeConfig = os.Getenv("KUBECONFIG_OUT")
	var config map[string]interface{}
	err := yaml.Unmarshal([]byte(c.KubeConfig), &config)
	require.NoError(t, err)
	suite.Run(t, &TestSuite{Cluster: c})
}

// aks uses vms and aks-engine uses vm for master and vmss for workers

func (s *TestSuite) TestARMTagToNodeLabel() {
	assert := assert.New(s.T())
	require := require.New(s.T())

	tags := map[string]*string{
		"fruit1": to.StringPtr("watermelon"),
		"fruit2": to.StringPtr("dragonfruit"),
		"fruit3": to.StringPtr("banana"),
	}

	// update configmap
	configOptions := s.GetConfigOptions()
	configOptions.SyncDirection = controller.ARMToNode
	configOptions.LabelPrefix = controller.DefaultLabelPrefix
	configOptions.ConflictPolicy = controller.ARMPrecedence
	configOptions.MinSyncPeriod = "1m"
	s.UpdateConfigOptions(configOptions)
	// do I need time to make sure this updates? like that it reaches the next reconcile in case minSyncPeriod was long

	// get compute resource
	computeResource := s.NewComputeResourceClient()

	// get nodes
	nodeList := s.GetNodes()

	// get number of tags
	numStartingTags := len(computeResource.Tags())

	// get number of labels on each node
	numStartingLabels := s.GetNumLabelsPerNode(nodeList)

	computeResourceNodes := s.GetNodesOnAzComputeResource(computeResource, nodeList)

	// check that every tag is a label (if it's convertible to a valid label name)

	// update tags
	// could this cause any issue with other updates that are maybe happening with tags that already existed?
	computeResource = s.UpdateTagsOnAzComputeResource(computeResource, tags)
	// check that computeResource tags have been updated
	for key, val := range tags {
		result, ok := computeResource.Tags()[key]
		assert.True(ok)
		assert.Equal(*val, *result)
	}
	s.T().Logf("Updated tags on Azure compute resource %s", computeResource.Name())

	// wait for labels to update
	time.Sleep(90 * time.Second) // assuming configmap has 1m minSyncPeriod

	// check that nodes now have accurate labels
	s.T().Logf("Checking nodes for accurate labels")
	s.CheckNodeLabelsForTags(computeResourceNodes, tags, numStartingLabels)

	// reset configmap first?

	// clean up compute resource by deleting tags
	computeResource = s.CleanupAzComputeResource(computeResource, tags, numStartingTags)

	time.Sleep(90 * time.Second) // wait for labels to be removed, assuming minSyncPeriod=1m

	// check that corresponding labels were deleted
	err := s.client.List(context.Background(), nodeList)
	require.NoError(err)
	for key := range tags {
		validLabelName := controller.ConvertTagNameToValidLabelName(key, controller.DefaultConfigOptions())
		for _, node := range nodeList.Items { // also checking none of nodes on other compute resource were affected
			// check that tag was deleted
			_, ok := node.Labels[validLabelName]
			assert.False(ok)
		}
	}
	for _, node := range nodeList.Items {
		// Checking to see if original labels are there.
		assert.Equal(numStartingLabels[node.Name], len(node.Labels))
	}
}

func (s *TestSuite) TestNodeLabelToARMTag() {
	assert := assert.New(s.T())
	require := require.New(s.T())

	labels := map[string]string{
		"veg1": "zucchini",
		"veg2": "swiss-chard",
		"veg3": "jalapeno",
	}

	// update config map
	configOptions := s.GetConfigOptions()
	configOptions.SyncDirection = controller.NodeToARM
	configOptions.LabelPrefix = controller.DefaultLabelPrefix
	configOptions.ConflictPolicy = controller.ARMPrecedence
	configOptions.MinSyncPeriod = "1m"
	s.UpdateConfigOptions(configOptions)

	// get tags
	computeResource := s.NewComputeResourceClient()

	// get nodes
	nodeList := s.GetNodes()

	numStartingTags := len(computeResource.Tags()) // I should probably do this before setting config map

	// get number of labels on each node
	numStartingLabels := s.GetNumLabelsPerNode(nodeList)

	// get only nodes on the chosen compute resource
	computeResourceNodes := s.GetNodesOnAzComputeResource(computeResource, nodeList)

	// update node labels
	for _, node := range computeResourceNodes {
		for key, val := range labels {
			node.Labels[key] = val
		}
		err := s.client.Update(context.Background(), &node)
		require.NoError(err)
	}
	s.T().Logf("Updated node labels")

	// wait for tags to update
	time.Sleep(90 * time.Second)

	// check that compute resource has accurate labels
	s.T().Logf("Checking Azure compute resource for accurate labels")
	// assert.Equal(len(labels), len(vmss.Tags)) // should check each node, current size - starting size
	s.CheckAzComputeResourceTagsForLabels(computeResource, labels)

	// reset configmap first?

	// clean up compute resource by deleting tags
	// if I implement deleting labels from vmss, then this will need to be a check instead of removing them
	for key := range labels {
		delete(computeResource.Tags(), key)
	}
	err := computeResource.Update(context.Background())
	require.NoError(err)
	// s.CleanupVMSS(&vmssClient, vmss, tags)
	assert.Equal(numStartingTags, len(computeResource.Tags()))
	assert.Equal(len(computeResource.Tags()), 0)

	// clean up nodes by deleting labels
	s.CleanupNodes(computeResourceNodes, labels)
	for _, node := range computeResourceNodes {
		assert.Equal(numStartingLabels[node.Name], len(node.Labels)) // might not be true yet?
	}
	s.T().Logf("Deleted test labels on nodes: %s", computeResource.Name())
}

func (s *TestSuite) TestTwoWaySync() {
	assert := assert.New(s.T())
	require := require.New(s.T())

	tags := map[string]*string{
		"favveg":    to.StringPtr("broccoli"),
		"favanimal": to.StringPtr("gopher"),
	}

	labels := map[string]string{
		"favfruit": "banana",
		"favfungi": "shiitake_mushroom",
	}

	// update config map
	configOptions := s.GetConfigOptions()
	configOptions.SyncDirection = controller.TwoWay
	configOptions.LabelPrefix = controller.DefaultLabelPrefix
	configOptions.ConflictPolicy = controller.ARMPrecedence
	configOptions.MinSyncPeriod = "1m"
	s.UpdateConfigOptions(configOptions)

	// get compute resource
	computeResource := s.NewComputeResourceClient()

	// get nodes
	nodeList := s.GetNodes()

	numStartingTags := len(computeResource.Tags())

	// get number of labels on each node
	numStartingLabels := s.GetNumLabelsPerNode(nodeList)

	computeResourceNodes := s.GetNodesOnAzComputeResource(computeResource, nodeList)

	// update tags
	computeResource = s.UpdateTagsOnAzComputeResource(computeResource, tags)
	// check that vmss tags have been updated
	for key, val := range tags {
		result, ok := computeResource.Tags()[key]
		assert.True(ok)
		assert.Equal(*result, *val)
	}
	s.T().Logf("Updated Azure compute resource tags")

	// update node labels
	for _, node := range computeResourceNodes {
		for key, val := range labels {
			node.Labels[key] = val
		}
		err := s.client.Update(context.Background(), &node)
		require.NoError(err)
	}

	// check tags
	s.CheckAzComputeResourceTagsForLabels(computeResource, labels)

	// check labels
	s.CheckNodeLabelsForTags(computeResourceNodes, tags, numStartingLabels)

	// cleanup configmap first

	// clean up vmss by deleting tags
	computeResource = s.CleanupAzComputeResource(computeResource, tags, numStartingTags)

	// clean up nodes by deleting labels
	s.CleanupNodes(computeResourceNodes, labels)
	for _, node := range computeResourceNodes {
		assert.Equal(numStartingLabels[node.Name], len(node.Labels)) // might not be true yet?
	}
	s.T().Logf("Deleted test labels on nodes: %s", computeResource.Name())

	// check that tags and labels got deleted off each other
	for key := range computeResource.Tags() {
		// assert not in tags
		_, ok := labels[key]
		assert.False(ok)
	}
	for _, node := range computeResourceNodes {
		// needs to be key without prefix
		for key := range node.Labels {
			_, ok := tags[controller.LabelWithoutPrefix(key, controller.DefaultLabelPrefix)]
			assert.False(ok)
		}
	}
}

func (s *TestSuite) TestInvalidTagsToLabels() {
	// tags
	_ = map[string]*string{
		"veg4": to.StringPtr("broccoli"),
		"veg5": to.StringPtr("brussels sprouts"), // invalid label value
	}
}

func (s *TestSuite) TestInvalidLabelsToTags() {
	// label
	_ = map[string]string{
		"k8s/role": "master", // invalid tag name
	}
}

// Helper functions

func (s *TestSuite) NewComputeResourceClient() controller.ComputeResource {
	if s.ResourceType == controller.VMSS {
		return s.NewVMSS()
	}
	return s.NewVM()
}

func (s *TestSuite) NewVMSS() controller.VirtualMachineScaleSet {
	assert := assert.New(s.T())
	require := require.New(s.T())

	vmssClient, err := azure.NewScaleSetClient(s.SubscriptionID) // I should check resource type here
	require.NoError(err)
	vmssList, err := vmssClient.List(context.Background(), s.ResourceGroup)
	if err != nil {
		s.T().Logf("Failed listing vmss in resource group %s: %q", s.ResourceGroup, err)
	}
	require.NoError(err)
	assert.NotEqual(0, len(vmssList.Values()))
	vmss := vmssList.Values()[0]
	vmss = controller.VMSSUserAssignedIdentity(vmss)
	s.T().Logf("Successfully found %d vmss: using %s", len(vmssList.Values()), *vmss.Name)
	return *controller.NewVMSSInitialized(context.Background(), s.ResourceGroup, &vmssClient, &vmss)
}

func (s *TestSuite) NewVM() controller.VirtualMachine {
	assert := assert.New(s.T())
	require := require.New(s.T())

	assert.True(s.ResourceType == controller.VM)
	vmClient, err := azure.NewVMClient(s.SubscriptionID)
	require.NoError(err)
	vmList, err := vmClient.List(context.Background(), s.ResourceGroup)
	if err != nil {
		s.T().Logf("Failed listing vms in resource group %s: %q", s.ResourceGroup, err)
	}
	require.NoError(err)
	assert.NotEqual(0, len(vmList.Values()))
	vm := vmList.Values()[0]
	vm = controller.VMUserAssignedIdentity(vm)
	s.T().Logf("Successfully found %d vms: using %s", len(vmList.Values()), *vm.Name)
	return *controller.NewVMInitialized(context.Background(), s.ResourceGroup, &vmClient, &vm)
}

func (s *TestSuite) GetConfigOptions() *controller.ConfigOptions {
	var configMap corev1.ConfigMap
	optionsNamespacedName := controller.OptionsConfigMapNamespacedName() // assuming "node-label-operator" and "node-label-operator-system", is this okay
	err := s.client.Get(context.Background(), optionsNamespacedName, &configMap)
	require.NoError(s.T(), err)
	configOptions, err := controller.NewConfigOptions(configMap)
	require.NoError(s.T(), err)

	return configOptions
}

func (s *TestSuite) UpdateConfigOptions(configOptions *controller.ConfigOptions) {
	configMap, err := controller.GetConfigMapFromConfigOptions(configOptions)
	require.NoError(s.T(), err)
	err = s.client.Update(context.Background(), &configMap)
	require.NoError(s.T(), err)
}

func (s *TestSuite) GetNodes() *corev1.NodeList {
	assert := assert.New(s.T())
	require := require.New(s.T())

	nodeList := &corev1.NodeList{}
	err := s.client.List(context.Background(), nodeList)
	if err != nil {
		s.T().Logf("Failed listing nodes: %s", err)
	}
	require.NoError(err)
	// should I somehow pass the expected number of nodes and check it here?
	assert.NotEqual(0, len(nodeList.Items))
	s.T().Logf("Successfully found %d nodes", len(nodeList.Items))

	return nodeList
}

func (s *TestSuite) GetNumLabelsPerNode(nodeList *corev1.NodeList) map[string]int {
	numLabels := map[string]int{}
	for _, node := range nodeList.Items {
		numLabels[node.Name] = len(node.Labels)
	}
	return numLabels
}

func (s *TestSuite) GetNodesOnAzComputeResource(computeResource controller.ComputeResource, nodeList *corev1.NodeList) []corev1.Node {
	computeResourceNodes := []corev1.Node{}
	for _, node := range nodeList.Items {
		// comparing values? Do I know vmss.ID is same format?
		provider, err := azure.ParseProviderID(node.Spec.ProviderID)
		require.NoError(s.T(), err)
		resource, err := azure.ParseProviderID(computeResource.ID())
		require.NoError(s.T(), err)
		if provider.ResourceType == resource.ResourceType && provider.ResourceName == resource.ResourceName {
			computeResourceNodes = append(computeResourceNodes, node)
		}
	}
	assert.NotEqual(s.T(), 0, len(computeResourceNodes))
	s.T().Logf("Found %d nodes on Azure compute resource %s", len(computeResourceNodes), computeResource.Name())

	return computeResourceNodes
}

func (s *TestSuite) UpdateTagsOnAzComputeResource(computeResource controller.ComputeResource, tags map[string]*string) controller.ComputeResource {
	for tag, val := range tags {
		computeResource.Tags()[tag] = val
	}
	err := computeResource.Update(context.Background())
	require.NoError(s.T(), err)

	return computeResource
}

func (s *TestSuite) CheckNodeLabelsForTags(nodes []corev1.Node, tags map[string]*string, numStartingLabels map[string]int) {
	for _, node := range nodes {
		updatedNode := &corev1.Node{}
		err := s.client.Get(context.Background(), types.NamespacedName{Name: node.Name, Namespace: node.Namespace}, updatedNode)
		require.NoError(s.T(), err)
		assert.Equal(s.T(), len(tags), len(updatedNode.Labels)-numStartingLabels[updatedNode.Name])
		for key, val := range tags {
			validLabelName := controller.ConvertTagNameToValidLabelName(key, controller.DefaultConfigOptions()) // make sure this is config options I use
			result, ok := updatedNode.Labels[validLabelName]
			assert.True(s.T(), ok)
			assert.Equal(s.T(), *val, result)
		}
	}
}

func (s *TestSuite) CheckAzComputeResourceTagsForLabels(computeResource controller.ComputeResource, labels map[string]string) {
	for key, val := range labels {
		v, ok := computeResource.Tags()[key]
		assert.True(s.T(), ok) // this is failing, or maybe it was the next line?
		assert.Equal(s.T(), val, *v)
	}
}

func (s *TestSuite) CleanupAzComputeResource(computeResource controller.ComputeResource, tags map[string]*string, numStartingTags int) controller.ComputeResource {
	for key := range tags {
		delete(computeResource.Tags(), key)
	}
	err := computeResource.Update(context.Background())
	require.NoError(s.T(), err)
	assert.Equal(s.T(), numStartingTags, len(computeResource.Tags())) // is this always true? two-way sync?
	s.T().Logf("Deleted test tags on Azure compute resource %s", computeResource.Name())

	return computeResource
}

func (s *TestSuite) CleanupNodes(vmssNodes []corev1.Node, labels map[string]string) {
	for _, node := range vmssNodes {
		for key := range labels {
			_, ok := node.Labels[key]
			assert.True(s.T(), ok)
			delete(node.Labels, key)
		}
		err := s.client.Update(context.Background(), &node)
		require.NoError(s.T(), err)
	}
}

func (s *TestSuite) ResetConfigOptions() {
	// minSyncPeriod is 5m, is that what I want??
	configMap := controller.DefaultConfigOptions()
	s.UpdateConfigOptions(&configMap)
}

// test:
// too many tags or labels
// resource group filter??
