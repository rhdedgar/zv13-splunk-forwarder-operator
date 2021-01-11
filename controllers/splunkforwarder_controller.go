/*
Copyright 2021.

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

package controllers

import (
	"context"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	managedv1alpha1 "github.com/rhdedgar/zv13-splunk-forwarder-operator/api/v1alpha1"
	"github.com/rhdedgar/zv13-splunk-forwarder-operator/config"
	sfv1alpha1 "github.com/rhdedgar/zv13-splunk-forwarder-operator/pkg/apis/splunkforwarder/v1alpha1"
	"github.com/rhdedgar/zv13-splunk-forwarder-operator/pkg/kube"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// SplunkForwarderReconciler reconciles a SplunkForwarder object
type SplunkForwarderReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=managed.openshift.io,resources=splunkforwarders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=managed.openshift.io,resources=splunkforwarders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=managed.openshift.io,resources=splunkforwarders/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SplunkForwarder object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *SplunkForwarderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reqLogger := r.Log.WithValues("splunkforwarder", req.NamespacedName)
	reqLogger.Info("Reconciling SplunkForwarder")

	// Fetch the SplunkForwarder instance
	instance := &sfv1alpha1.SplunkForwarder{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// See if our Secret exists
	secFound := &corev1.Secret{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: config.SplunkAuthSecretName, Namespace: request.Namespace}, secFound)
	if err != nil {
		return reconcile.Result{}, err
	}

	var clusterid string
	if instance.Spec.ClusterID != "" {
		clusterid = instance.Spec.ClusterID
	} else {
		configFound := &configv1.Infrastructure{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: "cluster"}, configFound)
		if err != nil {
			r.reqLogger.Info(err.Error())
			clusterid = "openshift"
		} else {
			clusterid = configFound.Status.InfrastructureName
		}
	}

	// ConfigMaps
	// Define a new ConfigMap object
	// TODO(wshearn) - check instance.Spec.ClusterID, if it is empty look it up on the cluster.
	configMaps := kube.GenerateConfigMaps(instance, request.NamespacedName, clusterid)
	if instance.Spec.UseHeavyForwarder {
		configMaps = append(configMaps, kube.GenerateInternalConfigMap(instance, request.NamespacedName))
		configMaps = append(configMaps, kube.GenerateFilteringConfigMap(instance, request.NamespacedName))

	}

	for _, configmap := range configMaps {
		// Set SplunkForwarder instance as the owner and controller
		if err := controllerutil.SetControllerReference(instance, configmap, r.scheme); err != nil {
			return reconcile.Result{}, err
		}

		// Check if this ConfigMap already exists
		cmFound := &corev1.ConfigMap{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: configmap.Name, Namespace: configmap.Namespace}, cmFound)
		if err != nil && errors.IsNotFound(err) {
			r.reqLogger.Info("Creating a new ConfigMap", "ConfigMap.Namespace", configmap.Namespace, "ConfigMap.Name", configmap.Name)
			err = r.client.Create(context.TODO(), configmap)
			if err != nil {
				return reconcile.Result{}, err
			}
		} else if err != nil {
			return reconcile.Result{}, err
		} else if instance.CreationTimestamp.After(cmFound.CreationTimestamp.Time) || r.CheckGenerationVersionOlder(cmFound.GetAnnotations(), instance) {
			r.reqLogger.Info("Updating ConfigMap", "ConfigMap.Namespace", configmap.Namespace, "ConfigMap.Name", configmap.Name)
			err = r.client.Update(context.TODO(), configmap)
			if err != nil {
				return reconcile.Result{}, err
			}
			return reconcile.Result{Requeue: true}, nil
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SplunkForwarderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&managedv1alpha1.SplunkForwarder{}).
		Owns(&managedv1alpha1.SplunkForwarder{}).
		Complete(r)
}
