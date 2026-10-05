package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
)

// +kubebuilder:rbac:groups=records.crossplane.io,resources=schemas;clusterschemas;records;recordsets,verbs=get;list;watch
// +kubebuilder:rbac:groups=records.crossplane.io,resources=schemas/status;clusterschemas/status;records/status;recordsets/status,verbs=get;update;patch

// Setup registers every Records controller with the manager.
func Setup(ctx context.Context, mgr ctrl.Manager) error {
	c := mgr.GetClient()
	if err := (&SchemaReconciler{Client: c}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&SchemaReconciler{Client: c, Cluster: true}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&RecordReconciler{Client: c}).SetupWithManager(ctx, mgr); err != nil {
		return err
	}
	return (&RecordSetReconciler{Client: c}).SetupWithManager(ctx, mgr)
}
