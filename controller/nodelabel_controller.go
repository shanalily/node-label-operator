// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT license.

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2019-03-01/compute"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/Azure/node-label-operator/azure"
)

const (
	VM   string = "virtualMachines"
	VMSS string = "virtualMachineScaleSets"
)

type ReconcileNodeLabel struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	LastUpdated map[string]time.Time // time each node was last updated
	Interval    time.Duration
	ctx         context.Context
	lock        sync.Mutex
}

// ComputeResource is a compute resource such as a Virtual Machine that
// should have its labels propagated to nodes running on the compute resource
type ComputeResource interface {
	// Get(ctx context.Context, name string) (azure.Spec, error)
	Update(ctx context.Context) error
	Tags() map[string]*string
	SetTag(name string, value *string)
}

type VirtualMachine struct {
	group  string
	client *compute.VirtualMachinesClient
	vm     *compute.VirtualMachine
}

func NewVM(ctx context.Context, subscriptionID, resourceGroup, resourceName string) (*VirtualMachine, error) {
	client, err := azure.NewVMClient(subscriptionID)
	if err != nil {
		return nil, err
	}
	vm, err := client.Get(ctx, resourceGroup, resourceName, compute.InstanceView)
	if err != nil {
		return nil, err
	}

	vm = VMUserAssignedIdentity(vm)

	return &VirtualMachine{group: resourceGroup, client: &client, vm: &vm}, nil
}

func (m VirtualMachine) Get(ctx context.Context, name string) (compute.VirtualMachine, error) {
	vm, err := m.client.Get(ctx, m.group, name, compute.InstanceView)
	if err != nil {
		return vm, err
	}

	vm = VMUserAssignedIdentity(vm)

	return vm, nil
}

func (m VirtualMachine) Update(ctx context.Context) error {
	f, err := m.client.CreateOrUpdate(ctx, m.group, *m.vm.Name, *m.vm)
	if err != nil {
		return err
	}

	if err := f.WaitForCompletionRef(ctx, m.client.Client); err != nil {
		return err
	}

	vm, err := f.Result(*m.client)
	if err != nil {
		return err
	}

	m.vm = &vm
	return nil
}

func (m VirtualMachine) Tags() map[string]*string {
	return m.vm.Tags
}

func (m VirtualMachine) SetTag(name string, value *string) {
	m.vm.Tags[name] = value
}

func VMUserAssignedIdentity(vm compute.VirtualMachine) compute.VirtualMachine {
	if vm.Identity != nil {
		vm.Identity.Type = compute.ResourceIdentityTypeUserAssigned
		for id, _ := range vm.Identity.UserAssignedIdentities {
			vm.Identity.UserAssignedIdentities[id] = &compute.VirtualMachineIdentityUserAssignedIdentitiesValue{}
		}
	}
	return vm
}

type VirtualMachineScaleSet struct {
	group  string
	client *compute.VirtualMachineScaleSetsClient
	vmss   *compute.VirtualMachineScaleSet
}

func NewVMSS(ctx context.Context, subscriptionID, resourceGroup, resourceName string) (*VirtualMachineScaleSet, error) {
	client, err := azure.NewScaleSetClient(subscriptionID)
	if err != nil {
		return nil, err
	}
	vmss, err := client.Get(ctx, resourceGroup, resourceName)
	if err != nil {
		return nil, err
	}

	vmss = VMSSUserAssignedIdentity(vmss)

	return &VirtualMachineScaleSet{group: resourceGroup, client: &client, vmss: &vmss}, nil
}

// find a way to actually use get??
func (m VirtualMachineScaleSet) Get(ctx context.Context, name string) (compute.VirtualMachineScaleSet, error) {
	vmss, err := m.client.Get(ctx, m.group, name)
	if err != nil {
		return compute.VirtualMachineScaleSet{}, err
	}

	vmss = VMSSUserAssignedIdentity(vmss)

	return vmss, nil
}

// does this work the wayw it's supposed to?
func (m VirtualMachineScaleSet) Update(ctx context.Context) error {
	f, err := m.client.CreateOrUpdate(ctx, m.group, *m.vmss.Name, *m.vmss)
	if err != nil {
		return err
	}

	if err := f.WaitForCompletionRef(ctx, m.client.Client); err != nil {
		return err
	}

	vmss, err := f.Result(*m.client)
	if err != nil {
		return err
	}

	m.vmss = &vmss
	return nil
}

func (m VirtualMachineScaleSet) Tags() map[string]*string {
	return m.vmss.Tags
}

func (m VirtualMachineScaleSet) SetTag(name string, value *string) {
	m.vmss.Tags[name] = value
}

func VMSSUserAssignedIdentity(vmss compute.VirtualMachineScaleSet) compute.VirtualMachineScaleSet {
	if vmss.Identity != nil { // is this an error otherwise?
		vmss.Identity.Type = compute.ResourceIdentityTypeUserAssigned
		for id, _ := range vmss.Identity.UserAssignedIdentities {
			vmss.Identity.UserAssignedIdentities[id] = &compute.VirtualMachineScaleSetIdentityUserAssignedIdentitiesValue{}
		}
	}
	return vmss
}

// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=nodes/status,verbs=get
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch

func (r *ReconcileNodeLabel) Reconcile(req reconcile.Request) (reconcile.Result, error) {
	r.ctx = context.Background()
	log := r.Log.WithValues("node-label-operator", req.NamespacedName)

	// check last updated, if updated too recently then wait
	intervalStart := time.Now().Add(-r.Interval)
	updateTimestamp, ok := r.LastUpdated[req.Name]
	if ok && !updateTimestamp.Before(intervalStart) {
		return ctrl.Result{}, nil
	}

	var configMap corev1.ConfigMap
	var configOptions ConfigOptions
	optionsNamespacedName := OptionsConfigMapNamespacedName() // assuming "node-label-operator" and "node-label-operator-system", is this okay
	if err := r.Get(r.ctx, optionsNamespacedName, &configMap); err != nil {
		log.V(1).Info("unable to fetch ConfigMap, instead using default configuration settings")
		// create default options config map and requeue
		configMap, err := NewDefaultConfigOptions()
		if err != nil {
			log.Error(err, "failed to get new default options configmap")
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
		}
		if err := r.Create(r.ctx, configMap); err != nil {
			log.Error(err, "failed to create new default options configmap")
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
		}
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil // do I return anything different?
	} else {
		configOptions, err = NewConfigOptions(configMap) // ConfigMap.Data is string -> string but I don't always want that
		if err != nil {
			log.Error(err, "failed to load options from config file")
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
		}
	}
	log.V(1).Info("configOptions", "syncDirection", configOptions.SyncDirection)
	log.V(1).Info("configOptions", "tag prefix", configOptions.TagPrefix)
	log.V(1).Info("configOptions", "interval", configOptions.Interval)

	configInterval, err := time.ParseDuration(configOptions.Interval)
	if configInterval.Milliseconds() != r.Interval.Milliseconds() {
		r.SetInterval(configInterval)
	}

	var node corev1.Node
	if err := r.Get(r.ctx, req.NamespacedName, &node); err != nil {
		log.Error(err, "unable to fetch Node")
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}
	log.V(1).Info("provider info", "provider ID", node.Spec.ProviderID)
	provider, err := azure.ParseProviderID(node.Spec.ProviderID)
	if err != nil {
		log.Error(err, "invalid provider ID", "node", node.Name)
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}
	if configOptions.ResourceGroupFilter != DefaultResourceGroupFilter &&
		provider.ResourceGroup != configOptions.ResourceGroupFilter {
		log.V(1).Info("found node not in resource group filter", "resource group filter", configOptions.ResourceGroupFilter, "node", node.Name)
		return ctrl.Result{}, nil
	}

	// do I also remove tags that have been deleted?
	switch provider.ResourceType {
	case VMSS:
		// Add VMSS tags to node
		if err := r.reconcileVMSS(req.NamespacedName, &provider, &node, configOptions); err != nil {
			log.Error(err, "failed to apply tags to nodes")
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
		}
	case VM:
		// Add VM tags to node
		if err := r.reconcileVMs(req.NamespacedName, &provider, &node, configOptions); err != nil {
			log.Error(err, "failed to apply tags to nodes")
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
		}
	default:
		log.V(1).Info("unrecognized resource type", "resource type", provider.ResourceType)
	}

	r.SetLastUpdated(req.NamespacedName.Name)
	return ctrl.Result{}, nil
}

// pass VMSS -> tags info and assign to nodes on VMs (unless node already has label)
func (r *ReconcileNodeLabel) reconcileVMSS(namespacedName types.NamespacedName, provider *azure.Resource,
	node *corev1.Node, configOptions ConfigOptions) error {
	vmss, err := NewVMSS(r.ctx, provider.SubscriptionID, provider.ResourceGroup, provider.ResourceName)
	if err != nil {
		return err
	}

	if configOptions.SyncDirection == TwoWay || configOptions.SyncDirection == ARMToNode {
		// I should only update if there are changes to labels
		patch, err := r.applyTagsToNodes(namespacedName, *vmss, node, configOptions)
		if err != nil {
			return err
		}
		if patch != nil { // gross looking
			if err = r.Patch(r.ctx, node, client.ConstantPatch(types.MergePatchType, patch)); err != nil {
				return err
			}
		}
	}

	// assign all labels on Node to VMSS, if not already there
	if configOptions.SyncDirection == TwoWay || configOptions.SyncDirection == NodeToARM {
		// I should only update if there are changes to labels
		tags, err := r.applyLabelsToAzureResource(namespacedName, *vmss, node, configOptions)
		if err != nil {
			return err
		}
		if tags != nil {
			for key, val := range tags {
				vmss.SetTag(key, val)
			}
			if err = vmss.Update(r.ctx); err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *ReconcileNodeLabel) reconcileVMs(namespacedName types.NamespacedName, provider *azure.Resource,
	node *corev1.Node, configOptions ConfigOptions) error {
	vm, err := NewVM(r.ctx, provider.SubscriptionID, provider.ResourceGroup, provider.ResourceName)
	if err != nil {
		return err
	}

	if configOptions.SyncDirection == TwoWay || configOptions.SyncDirection == ARMToNode {
		patch, err := r.applyTagsToNodes(namespacedName, *vm, node, configOptions)
		if err != nil {
			return err
		}
		if patch != nil {
			if err = r.Patch(r.ctx, node, client.ConstantPatch(types.MergePatchType, patch)); err != nil {
				return err
			}
		}
	}

	if configOptions.SyncDirection == TwoWay || configOptions.SyncDirection == NodeToARM {
		tags, err := r.applyLabelsToAzureResource(namespacedName, *vm, node, configOptions)
		if err != nil {
			return err
		}
		if tags != nil {
			for key, val := range tags {
				vm.SetTag(key, val)
			}
			if err = vm.Update(r.ctx); err != nil {
				return err
			}
		}
	}

	return nil
}

// return patch with new labels, if any, otherwise return nil for no new labels or an error
func (r *ReconcileNodeLabel) applyTagsToNodes(namespacedName types.NamespacedName, computeResource ComputeResource, node *corev1.Node, configOptions ConfigOptions) ([]byte, error) {
	log := r.Log.WithValues("node-label-operator", namespacedName)

	changed := false
	for tagName, tagVal := range computeResource.Tags() {
		if !ValidLabelName(tagName) {
			log.V(0).Info("invalid label name", "tag name", tagName)
			continue
		}
		validLabelName := ConvertTagNameToValidLabelName(tagName, configOptions)
		labelVal, ok := node.Labels[validLabelName]
		if !ok {
			// add tag as label
			log.V(1).Info("applying tags to nodes", "tagName", tagName, "tagVal", *tagVal)
			node.Labels[validLabelName] = *tagVal
			changed = true
		} else if labelVal != *tagVal {
			switch configOptions.ConflictPolicy {
			case ARMPrecedence:
				// set label anyway
				log.V(1).Info("overriding existing node label with ARM tag", "tagName", tagName, "tagVal", tagVal)
				node.Labels[validLabelName] = *tagVal
				changed = true
			case NodePrecedence:
				// do nothing
				log.V(0).Info("name->value conflict found", "node label value", labelVal, "ARM tag value", *tagVal)
			case Ignore:
				// raise k8s event
				r.Recorder.Event(node, "Warning", "ConflictingTagLabelValues",
					fmt.Sprintf("ARM tag was not applied to node because a different value for '%s' already exists (%s != %s).", tagName, *tagVal, labelVal))
				log.V(0).Info("name->value conflict found, leaving unchanged", "label value", labelVal, "tag value", *tagVal)
			default:
				return nil, errors.New("unrecognized conflict policy")
			}
		}
	}

	if !changed { // to avoid unnecessary patching
		return nil, nil
	}

	patch, err := labelPatch(node.Labels)
	if err != nil {
		return nil, err
	}

	return patch, nil
}

func (r *ReconcileNodeLabel) applyLabelsToAzureResource(namespacedName types.NamespacedName, computeResource ComputeResource, node *corev1.Node, configOptions ConfigOptions) (map[string]*string, error) {
	log := r.Log.WithValues("node-label-operator", namespacedName)

	if len(computeResource.Tags()) > maxNumTags {
		log.V(0).Info("can't add any more tags", "number of tags", len(computeResource.Tags()))
		return computeResource.Tags(), nil
	}

	newTags := map[string]*string{}
	for labelName, labelVal := range node.Labels {
		if !ValidTagName(labelName, configOptions) {
			// log.V(1).Info("invalid tag name", "label name", labelName)
			continue
		}
		validTagName := ConvertLabelNameToValidTagName(labelName, configOptions)
		tagVal, ok := computeResource.Tags()[validTagName]
		if !ok {
			// add label as tag
			log.V(1).Info("applying labels to Azure resource", "labelName", labelName, "labelVal", labelVal)
			newTags[validTagName] = &labelVal
		} else if *tagVal != labelVal {
			switch configOptions.ConflictPolicy {
			case NodePrecedence:
				// set tag anyway
				log.V(1).Info("overriding existing ARM tag with node label", "labelName", labelName, "labelVal", labelVal)
				newTags[validTagName] = &labelVal
			case ARMPrecedence:
				// do nothing
				log.V(0).Info("name->value conflict found", "node label value", labelVal, "ARM tag value", *tagVal)
			case Ignore:
				// raise k8s event
				r.Recorder.Event(node, "Warning", "ConflictingTagLabelValues",
					fmt.Sprintf("node label was not applied to Azure resource because a different value for '%s' already exists (%s != %s).", labelName, labelVal, *tagVal))
				log.V(0).Info("name->value conflict found, leaving unchanged", "label value", labelVal, "tag value", *tagVal)
			default:
				return nil, errors.New("unrecognized conflict policy")
			}
		}
	}

	if len(newTags) == 0 { // if unchanged
		return nil, nil
	}

	return newTags, nil
}

func labelPatch(labels map[string]string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": labels,
		},
	})
}

func (r *ReconcileNodeLabel) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc:  updateFunc,
			CreateFunc:  createFunc,
			DeleteFunc:  deleteFunc,
			GenericFunc: genericFunc,
		}).
		Complete(r)
}

func (r *ReconcileNodeLabel) SetLastUpdated(nodeName string) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.LastUpdated[nodeName] = time.Now()
}

func (r *ReconcileNodeLabel) SetInterval(duration time.Duration) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.Interval = duration
}

// for predicate
// but how can I quickly check for vmss tags :(

// are updates going to create?
// true because node might have a new label I guess
func updateFunc(e event.UpdateEvent) bool {
	return true
}

// somehow there's a ton of create events
func createFunc(e event.CreateEvent) bool {
	return true
}

// return true because vmss might need to be updated?
func deleteFunc(e event.DeleteEvent) bool {
	return true
}

func genericFunc(e event.GenericEvent) bool {
	return false
}
