// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT license.

package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2019-03-01/compute"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/Azure/node-label-operator/azure"
	"github.com/Azure/node-label-operator/controller"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func Test(t *testing.T) {
	c := &Cluster{}
	c.KubeConfigPath = os.Getenv("KUBECONFIG_OUT")
	_, err := os.Stat(c.KubeConfigPath)
	require.True(t, !os.IsNotExist(err))
	suite.Run(t, &TestSuite{Cluster: c})
}

// I want to make these tests work for both VMs and VMSS! because I will be testing on both!
// aks uses vms and aks-engine uses vmss
// turns out aks-engine master is on a vm and aks-engine workers are on a vmss! what does thits mean for testing?

// should I add tags in the test? and then remove them?
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

	// get vmss
	vmssClient, err := azure.NewScaleSetClient(s.SubscriptionID) // I should check resource type here
	require.NoError(err)
	vmssList, err := vmssClient.List(context.Background(), s.ResourceGroup)
	if err != nil {
		s.T().Logf("Failed listing vmss in resource group %s: %q", s.ResourceGroup, err)
	}
	require.NoError(err)
	assert.NotEqual(0, len(vmssList.Values()))
	// double check it doesn't have 'controlplane' or 'master' in title?
	// probably not an issue with aks-engine (runs on same vmss) or aks ("no" master)
	vmss := vmssList.Values()[0]
	s.T().Logf("Successfully found %d VMSS: using %s", len(vmssList.Values()), *vmss.Name)

	// get nodes
	nodeList := s.GetNodes()
	s.T().Logf("Successfully found %d nodes", len(nodeList.Items))

	// get number of tags
	numStartingTags := len(vmss.Tags)

	// get number of labels on each node
	numStartingLabels := s.GetNumLabelsPerNode(nodeList)

	vmssNodes := s.GetNodesOnVMSS(&vmss, nodeList)
	s.T().Logf("Found %d nodes on vmss %s", len(vmssNodes), *vmss.Name)

	// check that every tag is a label (if it's convertible to a valid label name)

	// update tags
	vmss = s.UpdateTagsOnVMSS(&vmssClient, vmss, tags)
	// check that vmss tags have been updated
	for key, val := range tags {
		result, ok := vmss.Tags[key]
		assert.True(ok)
		assert.Equal(*val, *result)
	}
	s.T().Logf("Updated tags on vmss %s", *vmss.Name)

	// wait for labels to update
	time.Sleep(90 * time.Second) // assuming configmap has 1m minSyncPeriod

	// check that nodes now have accurate labels
	s.T().Logf("Checking nodes for accurate labels")
	for _, node := range vmssNodes {
		updatedNode := &corev1.Node{}
		err = s.client.Get(context.Background(), types.NamespacedName{Name: node.Name, Namespace: node.Namespace}, updatedNode)
		require.NoError(err)
		assert.Equal(len(tags), len(updatedNode.Labels)-numStartingLabels[updatedNode.Name])
		for key, val := range tags {
			validLabelName := controller.ConvertTagNameToValidLabelName(key, controller.DefaultConfigOptions()) // make sure this is config options I use
			result, ok := updatedNode.Labels[validLabelName]
			assert.True(ok)
			assert.Equal(*val, result)
		}
	}

	// reset configmap first?

	// clean up vmss by deleting tags
	vmss = s.CleanupVMSS(&vmssClient, vmss, tags)
	assert.Equal(numStartingTags, len(vmss.Tags)) // I didn't get tags again before doing this? actually I did
	s.T().Logf("Deleted test tags on vmss %s", *vmss.Name)

	time.Sleep(90 * time.Second) // wait for labels to be removed, assuming minSyncPeriod=1m

	// check that corresponding labels were deleted
	err = s.client.List(context.Background(), nodeList)
	require.NoError(err)
	for key := range tags {
		validLabelName := controller.ConvertTagNameToValidLabelName(key, controller.DefaultConfigOptions())
		for _, node := range nodeList.Items { // also checking none of nodes on other vmss were affected
			// check that tag was deleted
			_, ok := node.Labels[validLabelName]
			assert.False(ok)
		}
	}
	for _, node := range nodeList.Items {
		// Checking to see if original labels are there. Can I assume this is true?
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
	vmssClient, err := azure.NewScaleSetClient(s.SubscriptionID) // I should check resource type here
	require.NoError(err)
	vmssList, err := vmssClient.List(context.Background(), s.ResourceGroup)
	if err != nil {
		s.T().Logf("Failed listing vmss in resource group %s: %q", s.ResourceGroup, err)
	}
	require.NoError(err)
	assert.NotEqual(0, len(vmssList.Values()))
	vmss := vmssList.Values()[0]
	s.T().Logf("Successfully found %d VMSS: using %s", len(vmssList.Values()), *vmss.Name)

	// get nodes
	nodeList := s.GetNodes()
	s.T().Logf("Successfully found %d nodes", len(nodeList.Items))

	// numStartingTags := len(vmss.Tags)

	// get number of labels on each node
	numStartingLabels := s.GetNumLabelsPerNode(nodeList)

	// get only nodes on the chosen vmss
	vmssNodes := s.GetNodesOnVMSS(&vmss, nodeList)
	s.T().Logf("Found %d nodes on vmss %s", len(vmssNodes), *vmss.Name)

	// update node labels
	for _, node := range vmssNodes {
		for key, val := range labels {
			node.Labels[key] = val
		}
		err = s.client.Update(context.Background(), &node)
		require.NoError(err)
	}
	s.T().Logf("Updated node labels")

	// wait for tags to update
	time.Sleep(90 * time.Second)

	// check that vmss have accurate labels
	s.T().Logf("Checking vmss for accurate labels")
	// assert.Equal(len(labels), len(vmss.Tags)) // should check each node, current size - starting size
	for key, val := range labels {
		result, ok := vmss.Tags[key]
		assert.True(ok) // this is failing, or maybe it was the next line?
		assert.Equal(val, *result)
	}

	// reset configmap first?

	// clean up vmss by deleting tags
	// if I implement deleting labels from vmss, then this will need to be a check instead of removing them
	for key := range labels {
		delete(vmss.Tags, key)
	}
	f, err := vmssClient.CreateOrUpdate(context.Background(), s.ResourceGroup, *vmss.Name, vmss)
	require.NoError(err)
	err = f.WaitForCompletionRef(context.Background(), vmssClient.Client)
	require.NoError(err)
	updatedVmss, err := f.Result(vmssClient)
	require.NoError(err)
	// s.CleanupVMSS(&vmssClient, vmss, tags)
	// assert.Equal(numStartingTags, len(vmss.Tags)) // I didn't get tags again before doing this? actually I did
	assert.Equal(len(updatedVmss.Tags), 0)

	// clean up nodes by deleting labels
	s.CleanupNodes(vmssNodes, labels)
	for _, node := range vmssNodes {
		assert.Equal(numStartingLabels[node.Name], len(node.Labels)) // might not be true yet?
	}
	s.T().Logf("Deleted test labels on nodes: %s", *vmss.Name)
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

	// get vmss
	vmssClient, err := azure.NewScaleSetClient(s.SubscriptionID) // I should check resource type here
	require.NoError(err)
	vmssList, err := vmssClient.List(context.Background(), s.ResourceGroup)
	if err != nil {
		s.T().Logf("Failed listing vmss in resource group %s: %q", s.ResourceGroup, err)
	}
	require.NoError(err)
	assert.NotEqual(0, len(vmssList.Values()))
	vmss := vmssList.Values()[0]
	s.T().Logf("Successfully found %d VMSS: using %s", len(vmssList.Values()), *vmss.Name)

	// get nodes
	nodeList := s.GetNodes()
	s.T().Logf("Successfully found %d nodes", len(nodeList.Items))

	numStartingTags := len(vmss.Tags)

	// get number of labels on each node
	numStartingLabels := s.GetNumLabelsPerNode(nodeList)

	vmssNodes := s.GetNodesOnVMSS(&vmss, nodeList)
	s.T().Logf("Found %d nodes on vmss %s", len(vmssNodes), *vmss.Name)

	// update tags
	vmss = s.UpdateTagsOnVMSS(&vmssClient, vmss, tags)
	// check that vmss tags have been updated
	for key, val := range tags {
		result, ok := vmss.Tags[key]
		assert.True(ok)
		assert.Equal(*result, *val)
	}
	s.T().Logf("Updated vmss tags")

	// update node labels
	for _, node := range vmssNodes {
		for key, val := range labels {
			node.Labels[key] = val
		}
		err = s.client.Update(context.Background(), &node)
		require.NoError(err)
	}

	// check tags
	for key, val := range labels {
		// check it's on vmss
		v, ok := vmss.Tags[key]
		assert.True(ok)
		assert.Equal(val, *v)
	}

	// check labels
	for key, val := range tags {
		validLabelName := controller.ConvertTagNameToValidLabelName(key, controller.DefaultConfigOptions())
		for _, node := range vmssNodes {
			v, ok := node.Labels[validLabelName]
			assert.True(ok)
			assert.Equal(*val, v)
		}
	}

	// cleanup configmap first

	// clean up vmss by deleting tags
	vmss = s.CleanupVMSS(&vmssClient, vmss, tags)
	assert.Equal(numStartingTags, len(vmss.Tags)) // might not be true yet...
	s.T().Logf("Deleted test tags on vmss %s", *vmss.Name)

	// clean up nodes by deleting labels
	s.CleanupNodes(vmssNodes, labels)
	for _, node := range vmssNodes {
		assert.Equal(numStartingLabels[node.Name], len(node.Labels)) // might not be true yet?
	}
	s.T().Logf("Deleted test labels on nodes: %s", *vmss.Name)

	// check that tags and labels got deleted off each other
	for key := range vmss.Tags {
		// assert not in tags
		_, ok := labels[key]
		assert.False(ok)
	}
	for _, node := range vmssNodes {
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

// might not end up using this stuff but idk

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
	s.T().Logf("Successfully found %d VMSS: using %s", len(vmssList.Values()), *vmss.Name)
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
		s.T().Logf("Failed listing vmss in resource group %s: %q", s.ResourceGroup, err)
	}
	require.NoError(err)
	assert.NotEqual(0, len(vmList.Values()))
	vmss := vmList.Values()[0]
	s.T().Logf("Successfully found %d VMSS: using %s", len(vmList.Values()), *vmss.Name)
	return *controller.NewVMInitialized(context.Background(), s.ResourceGroup, &vmClient, &vmss)
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

	return nodeList
}

func (s *TestSuite) GetNumLabelsPerNode(nodeList *corev1.NodeList) map[string]int {
	numLabels := map[string]int{}
	for _, node := range nodeList.Items {
		numLabels[node.Name] = len(node.Labels)
	}
	return numLabels
}

func (s *TestSuite) GetNodesOnVMSS(vmss *compute.VirtualMachineScaleSet, nodeList *corev1.NodeList) []corev1.Node {
	assert := assert.New(s.T())
	require := require.New(s.T())

	vmssNodes := []corev1.Node{}
	for _, node := range nodeList.Items {
		// comparing values? Do I know vmss.ID is same format?
		provider, err := azure.ParseProviderID(node.Spec.ProviderID)
		require.NoError(err)
		resource, err := azure.ParseProviderID(*vmss.ID)
		require.NoError(err)
		if provider.ResourceType == resource.ResourceType && provider.ResourceName == resource.ResourceName {
			vmssNodes = append(vmssNodes, node)
		}
	}
	assert.NotEqual(0, len(vmssNodes))

	return vmssNodes
}

func (s *TestSuite) UpdateTagsOnVMSS(vmssClient *compute.VirtualMachineScaleSetsClient, vmss compute.VirtualMachineScaleSet,
	tags map[string]*string) compute.VirtualMachineScaleSet {
	for tag, val := range tags {
		vmss.Tags[tag] = val
	}
	f, err := vmssClient.CreateOrUpdate(context.Background(), s.ResourceGroup, *vmss.Name, vmss)
	require.NoError(s.T(), err)
	err = f.WaitForCompletionRef(context.Background(), vmssClient.Client)
	require.NoError(s.T(), err)
	vmss, err = f.Result(*vmssClient)
	require.NoError(s.T(), err)

	return vmss
}

func (s *TestSuite) CleanupVMSS(vmssClient *compute.VirtualMachineScaleSetsClient, vmss compute.VirtualMachineScaleSet,
	tags map[string]*string) compute.VirtualMachineScaleSet {
	for key := range tags {
		delete(vmss.Tags, key)
	}
	f, err := vmssClient.CreateOrUpdate(context.Background(), s.ResourceGroup, *vmss.Name, vmss)
	require.NoError(s.T(), err)
	err = f.WaitForCompletionRef(context.Background(), vmssClient.Client)
	require.NoError(s.T(), err)
	vmss, err = f.Result(*vmssClient)
	require.NoError(s.T(), err)

	return vmss
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

// I'm not sure how I'm going to test vms yet since I can't use the same cluster
// I think resource IDs might be different so important

// test:
// invalid label names
// too many tags or labels
// resource group filter??
// test vm
