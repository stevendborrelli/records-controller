package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// +kubebuilder:rbac:groups=records.crossplane.io,resources=schemas;clusterschemas;records;recordsets,verbs=get;list;watch
// +kubebuilder:rbac:groups=records.crossplane.io,resources=schemas/status;clusterschemas/status;records/status;recordsets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=records.crossplane.io,resources=clusterschemas,verbs=create

// Setup registers every Records controller with the manager, and provides the
// well-known rawobject-v1 ClusterSchema.
func Setup(ctx context.Context, mgr ctrl.Manager) error {
	c := mgr.GetClient()
	if err := mgr.Add(manager.RunnableFunc(provideRawObject(c, mgr.GetAPIReader()))); err != nil {
		return err
	}
	if err := (&SchemaReconciler{Client: c}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&SchemaReconciler{Client: c, Reader: mgr.GetAPIReader(), Cluster: true}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&RecordReconciler{Client: c}).SetupWithManager(ctx, mgr); err != nil {
		return err
	}
	return (&RecordSetReconciler{Client: c}).SetupWithManager(ctx, mgr)
}
