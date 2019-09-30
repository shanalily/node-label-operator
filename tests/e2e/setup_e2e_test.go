// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT license.

package tests

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/node-label-operator/azure"
	"github.com/Azure/node-label-operator/controller"
)

var Scheme = runtime.NewScheme()

func AddToScheme(scheme *runtime.Scheme) {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
}

type Cluster struct {
	KubeConfigPath string
	SubscriptionID string
	ResourceGroup  string
	ResourceType   string
}

type TestSuite struct {
	suite.Suite
	suite.TearDownAllSuite
	*Cluster
	client client.Client
}

// necessary?
func initialize(c *Cluster) error {
	if c.KubeConfigPath == "" {
		return errors.New("missing parameters: KubeConfigPath must be set")
	}
	// do I want to create the test cluster(s) here somehow?
	return nil
}

func loadConfigOrFail(t *testing.T, kubeconfig string) *rest.Config {
	c, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
		&clientcmd.ConfigOverrides{}).ClientConfig()
	require.NoError(t, err)
	return c
}

func (s *TestSuite) SetupSuite() {
	s.T().Logf("\nSetupSuite")
	err := initialize(s.Cluster)
	require.Nil(s.T(), err)
	AddToScheme(Scheme)
	cl, err := client.New(loadConfigOrFail(s.T(), s.KubeConfigPath), client.Options{Scheme: Scheme})
	require.NoError(s.T(), err)
	s.client = cl

	// better to get metadata endpoint?
	nodeList := &corev1.NodeList{}
	err = cl.List(context.Background(), nodeList, client.InNamespace("default"))
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), nodeList.Items)
	resource, err := azure.ParseProviderID(nodeList.Items[0].Spec.ProviderID)
	require.NoError(s.T(), err)

	s.SubscriptionID = resource.SubscriptionID
	s.ResourceGroup = resource.ResourceGroup
	s.ResourceType = resource.ResourceType // ends up depending on which node is chosen first for aks-engine
}

// is this doing anything?
func (s *TestSuite) TearDownSuite() {
	s.T().Logf("\nTearDownSuite")

	// make sure configmap is reset
	var configMap corev1.ConfigMap
	optionsNamespacedName := controller.OptionsConfigMapNamespacedName() // assuming "node-label-operator" and "node-label-operator-system", is this okay
	err := s.client.Get(context.Background(), optionsNamespacedName, &configMap)
	require.NoError(s.T(), err)
	configOptions, err := controller.NewConfigOptions(configMap)
	require.NoError(s.T(), err)
	configOptions.SyncDirection = controller.ARMToNode
	configOptions.LabelPrefix = controller.DefaultLabelPrefix
	configOptions.ConflictPolicy = controller.ARMPrecedence
	configOptions.MinSyncPeriod = "1m"
	configMap, err = controller.GetConfigMapFromConfigOptions(configOptions)
	require.NoError(s.T(), err)
	err = s.client.Update(context.Background(), &configMap)
	require.NoError(s.T(), err)

	// make sure necessary tags/labels deleted? I would maybe save current tags and current labels?

	s.T().Logf("Finished tearing down suite")
}