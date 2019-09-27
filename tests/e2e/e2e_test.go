// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT license.

package tests

import (
	"context"
	"time"

	"github.com/Azure/go-autorest/autorest/to"
	"github.com/Azure/node-label-operator/azure"
	"github.com/Azure/node-label-operator/controller"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// should I add tags in the test? and then remove them?
func (s *TestSuite) TestARMTagToNodeLabel() {
	assert := assert.New(s.T())
	require := require.New(s.T())

	tags := map[string]*string{
		"fruit1": to.StringPtr("watermelon"),
		"fruit2": to.StringPtr("dragonfruit"),
		"fruit3": to.StringPtr("banana"),
	}

	// make sure configmap is set up properly

	// get tags
	vmssClient, err := azure.NewScaleSetClient(s.SubscriptionID) // I should check resource type here
	require.NoError(err)
	vmssList, err := vmssClient.List(context.Background(), s.ResourceGroup)
	require.NoError(err)
	s.T().Log(vmssList.Values())
	assert.NotEqual(len(vmssList.Values()), 0)

	// get labels
	nodeList := &corev1.NodeList{}
	err = s.client.List(context.Background(), nodeList, client.InNamespace("node-label-operator"))
	require.NoError(err)
	assert.NotEqual(len(nodeList.Items), 0)
	numLabels := map[string]int{}
	for _, node := range nodeList.Items {
		numLabels[node.Name] = len(node.Labels)
	}

	// check that every tag is a label (if it's convertible to a valid label name)

	// update tags
	// perhaps I should only add tags to one vmss? then I would somehow have to get only nodes on that vmss
	for _, vmss := range vmssList.Values() {
		vmss.Tags = tags
		// update
		f, err := vmssClient.CreateOrUpdate(context.Background(), s.ResourceGroup, *vmss.Name, vmss)
		require.NoError(err)
		err = f.WaitForCompletionRef(context.Background(), vmssClient.Client)
		require.NoError(err)
		updatedVmss, err := f.Result(vmssClient)
		require.NoError(err)
		// check that vmss tags have been updated
		for key, val := range tags {
			result, ok := updatedVmss.Tags[key]
			assert.True(ok)
			assert.Equal(*result, *val)
		}
	}

	// wait for labels to update
	time.Sleep(5 * time.Minute) // make sure configmap minSyncPeriod is less than 5m

	// check that nodes now have accurate labels
	for _, node := range nodeList.Items {
		// this isn't necessarily true, nodes often have other labels
		// I should get difference of previous labels and added labels? are there other labels being added?
		assert.Equal(s.T(), len(tags), numLabels[node.Name]-len(node.Labels))
		for key, val := range tags {
			validLabelName := controller.ConvertTagNameToValidLabelName(key, controller.DefaultConfigOptions())
			result, ok := node.Labels[validLabelName]
			assert.True(ok)
			assert.Equal(result, val)
		}
	}

	// clean up vmss by deleting tags
	for _, vmss := range vmssList.Values() {
		vmss.Tags = map[string]*string{}
		// update
		f, err := vmssClient.CreateOrUpdate(context.Background(), s.ResourceGroup, *vmss.Name, vmss)
		require.NoError(err)
		err = f.WaitForCompletionRef(context.Background(), vmssClient.Client)
		require.NoError(err)
		updatedVmss, err := f.Result(vmssClient)
		require.NoError(err)
		assert.Equal(len(updatedVmss.Tags), 0)
	}

	time.Sleep(5 * time.Minute) // wait for labels to be removed

	// check that corresponding labels were deleted
	// do I have to "List" nodes again?
	for key := range tags {
		validLabelName := controller.ConvertTagNameToValidLabelName(key, controller.DefaultConfigOptions())
		for _, node := range nodeList.Items {
			// check that tag was deleted
			_, ok := node.Labels[validLabelName]
			assert.False(ok)
		}
	}
	for _, node := range nodeList.Items {
		// Checking to see if original labels are there. Can I assume this is true?
		assert.Equal(len(node.Labels), numLabels[node.Name])
	}
}

func (s *TestSuite) TestNodeLabelToARMTag() {
	assert := assert.New(s.T())
	require := require.New(s.T())

	labels := map[string]string{
		"veg1": "zucchini",
		"veg2": "swiss chard",
		"veg3": "jalapeno",
	}

	// make sure config map is set up properly?

	// get tags
	vmssClient, err := azure.NewScaleSetClient(s.SubscriptionID)
	require.NoError(err)
	vmssList, err := vmssClient.List(context.Background(), s.ResourceGroup)
	require.NoError(err)
	s.T().Log(vmssList.Values())
	assert.NotEqual(len(vmssList.Values()), 0)

	// get labels
	nodeList := &corev1.NodeList{}
	err = s.client.List(context.Background(), nodeList, client.InNamespace("node-label-operator"))
	require.NoError(err)
	assert.NotEqual(len(nodeList.Items), 0)

	// update node labels
	for _, node := range nodeList.Items {
		node.Labels = labels
		err = s.client.Update(context.Background(), &node)
		require.NoError(err)
	}

	// wait for tags to update
	time.Sleep(5 * time.Minute)

	// check that vmss have accurate labels
	for _, vmss := range vmssList.Values() {
		assert.Equal(s.T(), len(labels), len(vmss.Tags))
		for key, val := range labels {
			result, ok := vmss.Tags[key]
			assert.True(ok)
			assert.Equal(*result, val)
		}
	}

	// clean up vmss by deleting tags
	// if I implement deleting labels from vmss, then this will need to be a check instead of removing them
	for _, vmss := range vmssList.Values() {
		vmss.Tags = map[string]*string{}
		// update
		f, err := vmssClient.CreateOrUpdate(context.Background(), s.ResourceGroup, *vmss.Name, vmss)
		require.NoError(err)
		err = f.WaitForCompletionRef(context.Background(), vmssClient.Client)
		require.NoError(err)
		updatedVmss, err := f.Result(vmssClient)
		require.NoError(err)
		assert.Equal(s.T(), len(updatedVmss.Tags), 0)
	}

	// clean up nodes by deleting labels
	for _, node := range nodeList.Items {
		for key := range labels {
			_, ok := node.Labels[key]
			assert.True(ok)
			delete(node.Labels, key)
		}
	}
}

func (s *TestSuite) TestTwoWaySync() {
	assert := assert.New(s.T())
	require := require.New(s.T())

	tags := map[string]*string{
		"favveg":    to.StringPtr("broccoli"),
		"favanimal": to.StringPtr("gopher"),
	}

	// get vmss
	vmssClient, err := azure.NewScaleSetClient(s.SubscriptionID)
	require.NoError(err)
	vmssList, err := vmssClient.List(context.Background(), s.ResourceGroup)
	require.NoError(err)
	s.T().Log(vmssList.Values())
	assert.NotEqual(len(vmssList.Values()), 0)

	// update tags
	for _, vmss := range vmssList.Values() {
		vmss.Tags = tags
		// update
		f, err := vmssClient.CreateOrUpdate(context.Background(), s.ResourceGroup, *vmss.Name, vmss)
		require.NoError(err)
		err = f.WaitForCompletionRef(context.Background(), vmssClient.Client)
		require.NoError(err)
		updatedVmss, err := f.Result(vmssClient)
		require.NoError(err)
		// check that vmss tags have been updated
		for key, val := range tags {
			result, ok := updatedVmss.Tags[key]
			assert.True(ok)
			assert.Equal(*result, *val)
		}
	}

	labels := map[string]string{
		"favfruit": "banana",
		"favfungi": "shiitake mushroom",
	}

	// get nodes
	nodeList := &corev1.NodeList{}
	err = s.client.List(context.Background(), nodeList, client.InNamespace("node-label-operator"))
	require.NoError(err)
	assert.NotEqual(len(nodeList.Items), 0)

	// update node labels
	for _, node := range nodeList.Items {
		node.Labels = labels
		err = s.client.Update(context.Background(), &node)
		require.NoError(err)
	}

}

func (s *TestSuite) TestInvalidTagsToLabels() {
}

func (s *TestSuite) TestInvalidLabelsToTags() {
}

// I'm not sure how I'm going to test this yet since I can't use the same cluster
// I think resource IDs might be different so important
func (s *TestSuite) TestVMTagToNode() {
}

// test:
// invalid label names
// too many tags or labels
// resource group filter??
// test vm
