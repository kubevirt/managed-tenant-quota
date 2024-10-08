package mtq_operator

import (
	"context"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/utils"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func (r *ReconcileMTQ) watchMTQCRD() error {
	if err := r.controller.Watch(source.Kind(r.getCache(), &extv1.CustomResourceDefinition{}, handler.TypedEnqueueRequestsFromMapFunc[*extv1.CustomResourceDefinition](
		func(ctx context.Context, obj *extv1.CustomResourceDefinition) []reconcile.Request {
			name := obj.GetName()
			if name != "mtqs.mtq.kubevirt.io" {
				return nil
			}
			cr, err := utils.GetActiveMTQ(r.client)
			if err != nil {
				return nil
			}
			return []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{
						Namespace: "",
						Name:      cr.Name,
					},
				},
			}
		},
	))); err != nil {
		return err
	}

	return nil
}
