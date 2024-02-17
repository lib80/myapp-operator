/*
Copyright 2024 libin.

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
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appsv1alpha1 "github.com/lib80/myapp-operator/api/v1alpha1"
)

const (
	myappFinalizer         = "cache.leon.com/finalizer"
	genericRequeueDuration = time.Second * 3
	typeAvailableMyApp     = "Available"
	typeDegradedMyApp      = "Degraded"
)

// MyAppReconciler reconciles a MyApp object
type MyAppReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=apps.leon.com,resources=myapps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps.leon.com,resources=myapps/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps.leon.com,resources=myapps/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MyApp object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *MyAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting a reconcile.")
	app := &appsv1alpha1.MyApp{}
	if err := r.Get(ctx, req.NamespacedName, app); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("MyApp object not Found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get MyApp object, will requeue request after a short time.")
		return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
	}

	if app.Status.Conditions == nil || len(app.Status.Conditions) == 0 {
		meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
			Type:    typeAvailableMyApp,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		})
		if err := r.Status().Update(ctx, app); err != nil {
			logger.Error(err, "Failed to update MyApp resource status.")
			return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
		}
	}

	//Check if the MyApp instance is marked to be deleted, which is indicated by the deletion timestamp being set.
	if app.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(app, myappFinalizer) {
			logger.Info("Adding Finalizer for MyApp")
			if ok := controllerutil.AddFinalizer(app, myappFinalizer); !ok {
				logger.Error(nil, "Failed to add finalizer into the MyApp resource.")
				return ctrl.Result{RequeueAfter: genericRequeueDuration}, nil
			}
			if err := r.Update(ctx, app); err != nil {
				logger.Error(nil, "Failed to update custom resource to add finalizer")
				return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(app, myappFinalizer) {
			logger.Info("Performing Finalizer Operations before delete CR.")
			meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
				Type:    typeDegradedMyApp,
				Status:  metav1.ConditionUnknown,
				Reason:  "Finalizing",
				Message: "Performing finalizer operations",
			})
			if err := r.Status().Update(ctx, app); err != nil {
				logger.Error(err, "Failed to update MyApp resource status.")
				return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
			}

			if err := r.doFinalizerOperationsForMyApp(app); err != nil {
				return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
			}

			meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
				Type:    typeDegradedMyApp,
				Status:  metav1.ConditionTrue,
				Reason:  "Finalizing",
				Message: "Finalizer operations were successfully accomplished",
			})
			if err := r.Status().Update(ctx, app); err != nil {
				logger.Error(err, "Failed to update MyApp resource status.")
				return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
			}

			logger.Info("Removing Finalizer after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(app, myappFinalizer); !ok {
				logger.Error(nil, "Failed to remove finalizer for MyApp")
				return ctrl.Result{RequeueAfter: genericRequeueDuration}, nil
			}
			if err := r.Update(ctx, app); err != nil {
				logger.Error(err, "Failed to update custom resource to remove finalizer")
				return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
			}
		}
		return ctrl.Result{}, nil
	}

	logger.Info("Reconciling subresource Deployment.")
	if result, err := r.reconcileDeployment(ctx, app); err != nil {
		logger.Error(err, "Fail to reconcile Deployment.")
		meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
			Type:    typeAvailableMyApp,
			Status:  metav1.ConditionFalse,
			Reason:  "Reconciling",
			Message: "Failed to reconcil Deployment for the custom resource",
		})
		if err := r.Status().Update(ctx, app); err != nil {
			return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
		}
		return result, err
	}

	logger.Info("Reconciling subresource Service.")
	if result, err := r.reconcileService(ctx, app); err != nil {
		logger.Error(err, "Fail to reconcile Service.")
		meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
			Type:    typeAvailableMyApp,
			Status:  metav1.ConditionFalse,
			Reason:  "Reconciling",
			Message: "Failed to reconcile Service for the custom resource",
		})
		if err := r.Status().Update(ctx, app); err != nil {
			return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
		}
		return result, err
	}

	logger.Info("It has been reconciled.")
	meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
		Type:    typeAvailableMyApp,
		Status:  metav1.ConditionTrue,
		Reason:  "ReplicasAvailable",
		Message: "Custom resource is available",
	})
	if err := r.Status().Update(ctx, app); err != nil {
		return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
	}
	return ctrl.Result{}, nil
}

func (r *MyAppReconciler) reconcileDeployment(ctx context.Context, app *appsv1alpha1.MyApp) (ctrl.Result, error) {
	dp := &appsv1.Deployment{}
	//Check if the deployment already exists, if not create a new one.
	if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, dp); err != nil {
		if errors.IsNotFound(err) {
			if err := r.Create(ctx, r.newDeployment(app)); err != nil {
				return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
	}
	var needUpd bool
	if *dp.Spec.Replicas != *app.Spec.Replicas {
		dp.Spec.Replicas = app.Spec.Replicas
		needUpd = true
	}

	if dp.Spec.Template.Spec.Containers[0].Image != app.Spec.Image {
		dp.Spec.Template.Spec.Containers[0].Image = app.Spec.Image
		needUpd = true
	}

	containerPorts := dp.Spec.Template.Spec.Containers[0].Ports
	ports := make([]int32, 0, len(containerPorts))
	for _, containerPort := range containerPorts {
		ports = append(ports, containerPort.ContainerPort)
	}
	if !reflect.DeepEqual(ports, app.Spec.Ports) {
		newContainerPorts := make([]corev1.ContainerPort, 0, len(app.Spec.Ports))
		for _, port := range app.Spec.Ports {
			newContainerPorts = append(newContainerPorts, corev1.ContainerPort{ContainerPort: port})
		}
		dp.Spec.Template.Spec.Containers[0].Ports = newContainerPorts
		needUpd = true
	}
	if needUpd {
		if err := r.Update(ctx, dp); err != nil {
			return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *MyAppReconciler) reconcileService(ctx context.Context, app *appsv1alpha1.MyApp) (ctrl.Result, error) {
	svc := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, svc); err != nil {
		if errors.IsNotFound(err) {
			if err := r.Create(ctx, r.newService(app)); err != nil {
				return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
	}

	var needUpd bool
	servicePorts := svc.Spec.Ports
	ports := make([]int32, 0, len(servicePorts))
	for _, servicePort := range servicePorts {
		ports = append(ports, servicePort.Port)
	}
	if !reflect.DeepEqual(ports, app.Spec.Ports) {
		newServicePorts := make([]corev1.ServicePort, 0, len(app.Spec.Ports))
		for _, port := range app.Spec.Ports {
			newServicePorts = append(newServicePorts, corev1.ServicePort{Port: port})
		}
		svc.Spec.Ports = newServicePorts
		needUpd = true
	}
	if needUpd {
		if err := r.Update(ctx, svc); err != nil {
			return ctrl.Result{RequeueAfter: genericRequeueDuration}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *MyAppReconciler) newDeployment(app *appsv1alpha1.MyApp) *appsv1.Deployment {
	labels := map[string]string{"app": app.Name}
	containerPorts := make([]corev1.ContainerPort, 0, len(app.Spec.Ports))
	for _, port := range app.Spec.Ports {
		containerPorts = append(containerPorts, corev1.ContainerPort{ContainerPort: port})
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: app.Namespace,
			//除了使用 ctrl.SetControllerReference 方法设置OwnerReferences，也可以直接指定该字段的值
			//OwnerReferences: []metav1.OwnerReference{
			//	*metav1.NewControllerRef(app, appsv1alpha1.GroupVersion.WithKind("MyApp")),
			//},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: app.Spec.Replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  app.Name,
							Image: app.Spec.Image,
							Ports: containerPorts,
						},
					},
				},
			},
		},
	}
	_ = ctrl.SetControllerReference(app, dep, r.Scheme)
	return dep
}

func (r *MyAppReconciler) newService(app *appsv1alpha1.MyApp) *corev1.Service {
	servicePorts := make([]corev1.ServicePort, 0, len(app.Spec.Ports))
	for _, port := range app.Spec.Ports {
		servicePorts = append(servicePorts, corev1.ServicePort{Port: port})
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: app.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports:    servicePorts,
			Selector: map[string]string{"app": app.Name},
		},
	}
	_ = ctrl.SetControllerReference(app, svc, r.Scheme)
	return svc
}

func (r *MyAppReconciler) doFinalizerOperationsForMyApp(app *appsv1alpha1.MyApp) error {
	// Add the cleanup steps that the operator needs to do before the CR can be deleted.

	r.Recorder.Event(app, "Warning", "Deleting",
		fmt.Sprintf("Custom Resource %s is being deleted from the namespace %s", app.Name, app.Namespace))
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MyAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1alpha1.MyApp{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(createEvent event.CreateEvent) bool {
				return true
			},
			DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
				return false
			},
			UpdateFunc: func(updateEvent event.UpdateEvent) bool {
				if updateEvent.ObjectNew.GetResourceVersion() == updateEvent.ObjectOld.GetResourceVersion() {
					return false
				}
				//if reflect.DeepEqual(updateEvent.ObjectNew.(*appsv1alpha1.MyApp).Spec, updateEvent.ObjectOld.(*appsv1alpha1.MyApp).Spec) {
				//	return false
				//}
				return true
			},
		})).Owns(&appsv1.Deployment{}, builder.WithPredicates(predicate.Funcs{
		CreateFunc: func(createEvent event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
			return true
		},
		UpdateFunc: func(updateEvent event.UpdateEvent) bool {
			if updateEvent.ObjectNew.GetResourceVersion() == updateEvent.ObjectOld.GetResourceVersion() {
				return false
			}
			return true
		},
	})).Owns(&corev1.Service{}, builder.WithPredicates(predicate.Funcs{
		CreateFunc: func(createEvent event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
			return true
		},
		UpdateFunc: func(updateEvent event.UpdateEvent) bool {
			if updateEvent.ObjectNew.GetResourceVersion() == updateEvent.ObjectOld.GetResourceVersion() {
				return false
			}
			return true
		},
	})).Complete(r)
}
