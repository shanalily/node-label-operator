// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT license.

package options

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Azure/node-label-operator/labelsync/naming"
)

const (
	DefaultLabelPrefix         string = "azure.tags"
	DefaultTagPrefix           string = "node.labels"
	DefaultResourceGroupFilter string = "none"
	DefaultMinSyncPeriod       string = "5m"
	UNSET                      string = "unset"
)

type SyncDirection string

const (
	TwoWay    SyncDirection = "two-way"
	ARMToNode SyncDirection = "arm-to-node"
	NodeToARM SyncDirection = "node-to-arm"
)

type ConflictPolicy string

const (
	Ignore         ConflictPolicy = "ignore"
	ARMPrecedence  ConflictPolicy = "arm-precedence"
	NodePrecedence ConflictPolicy = "node-precedence"
)

type ConfigOptions struct {
	SyncDirection       SyncDirection  `json:"syncDirection"`
	LabelPrefix         string         `json:"labelPrefix"`
	TagPrefix           string         `json:"tagPrefix"`
	ConflictPolicy      ConflictPolicy `json:"conflictPolicy"`
	ResourceGroupFilter string         `json:"resourceGroupFilter"`
	MinSyncPeriod       string         `json:"minSyncPeriod"`
}

func NewConfig(configMap corev1.ConfigMap) (*ConfigOptions, error) {
	configOptions, err := LoadConfigOptionsFromConfigMap(configMap)
	if err != nil {
		return nil, err
	}

	if configOptions.SyncDirection == "" {
		configOptions.SyncDirection = ARMToNode
	} else if configOptions.SyncDirection != TwoWay &&
		configOptions.SyncDirection != ARMToNode &&
		configOptions.SyncDirection != NodeToARM {
		return nil, errors.New("invalid sync direction")
	}

	if configOptions.LabelPrefix == UNSET {
		configOptions.LabelPrefix = DefaultLabelPrefix
	} else if len(configOptions.LabelPrefix) > naming.MaxLabelPrefixLen {
		return nil, fmt.Errorf(fmt.Sprintf("label prefix is over %d characters", naming.MaxLabelPrefixLen))
	}

	// also validate prefix?
	if configOptions.TagPrefix == UNSET {
		configOptions.TagPrefix = DefaultTagPrefix
	}

	if configOptions.ConflictPolicy == "" {
		configOptions.ConflictPolicy = ARMPrecedence
	} else if configOptions.ConflictPolicy != Ignore &&
		configOptions.ConflictPolicy != ARMPrecedence &&
		configOptions.ConflictPolicy != NodePrecedence {
		return nil, errors.New("invalid tag-to-label conflict policy")
	}

	if configOptions.ResourceGroupFilter == "" {
		configOptions.ResourceGroupFilter = DefaultResourceGroupFilter
	}

	if configOptions.MinSyncPeriod == "" {
		configOptions.MinSyncPeriod = DefaultMinSyncPeriod
	} else if _, err = time.ParseDuration(configOptions.MinSyncPeriod); err != nil {
		return nil, err
	}

	return &configOptions, nil
}

func NewDefaultConfig() (*corev1.ConfigMap, error) {
	configOptions := DefaultConfigOptions()
	configMap, err := GetConfigMapFromConfigOptions(&configOptions)
	if err != nil {
		return nil, err
	}
	return &configMap, nil
}

func DefaultConfigOptions() ConfigOptions {
	return ConfigOptions{
		SyncDirection:       ARMToNode,
		LabelPrefix:         DefaultLabelPrefix,
		TagPrefix:           DefaultTagPrefix,
		ConflictPolicy:      ARMPrecedence,
		ResourceGroupFilter: DefaultResourceGroupFilter,
		MinSyncPeriod:       DefaultMinSyncPeriod,
	}
}

// ConfigMap -> ConfigOptions
func LoadConfigOptionsFromConfigMap(configMap corev1.ConfigMap) (ConfigOptions, error) {
	data, err := json.Marshal(configMap.Data)
	if err != nil {
		return ConfigOptions{}, err
	}

	configOptions := ConfigOptions{LabelPrefix: UNSET, TagPrefix: UNSET}
	if err := json.Unmarshal(data, &configOptions); err != nil {
		return ConfigOptions{}, err
	}

	return configOptions, nil
}

// ConfigOptions -> ConfigMap
func GetConfigMapFromConfigOptions(configOptions *ConfigOptions) (corev1.ConfigMap, error) {
	b, err := json.Marshal(configOptions)
	if err != nil {
		return corev1.ConfigMap{}, err
	}

	configMap := corev1.ConfigMap{}
	if err := json.Unmarshal(b, &configMap.Data); err != nil {
		return corev1.ConfigMap{}, nil
	}
	namespacedName := ConfigMapNamespacedName()
	configMap.Name = namespacedName.Name
	configMap.Namespace = namespacedName.Namespace

	return configMap, nil
}

func ConfigMapNamespacedName() types.NamespacedName {
	return types.NamespacedName{Name: "node-label-operator", Namespace: "node-label-operator-system"}
}
