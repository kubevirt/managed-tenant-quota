/*
Copyright 2023 The MTQ Authors.

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

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	"context"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
	v1alpha1 "kubevirt.io/managed-tenant-quota/staging/src/kubevirt.io/managed-tenant-quota-api/pkg/apis/core/v1alpha1"
)

// FakeMTQs implements MTQInterface
type FakeMTQs struct {
	Fake *FakeMtqV1alpha1
}

var mtqsResource = v1alpha1.SchemeGroupVersion.WithResource("mtqs")

var mtqsKind = v1alpha1.SchemeGroupVersion.WithKind("MTQ")

// Get takes name of the mTQ, and returns the corresponding mTQ object, and an error if there is any.
func (c *FakeMTQs) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha1.MTQ, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootGetAction(mtqsResource, name), &v1alpha1.MTQ{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.MTQ), err
}

// List takes label and field selectors, and returns the list of MTQs that match those selectors.
func (c *FakeMTQs) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha1.MTQList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootListAction(mtqsResource, mtqsKind, opts), &v1alpha1.MTQList{})
	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1alpha1.MTQList{ListMeta: obj.(*v1alpha1.MTQList).ListMeta}
	for _, item := range obj.(*v1alpha1.MTQList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested mTQs.
func (c *FakeMTQs) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewRootWatchAction(mtqsResource, opts))
}

// Create takes the representation of a mTQ and creates it.  Returns the server's representation of the mTQ, and an error, if there is any.
func (c *FakeMTQs) Create(ctx context.Context, mTQ *v1alpha1.MTQ, opts v1.CreateOptions) (result *v1alpha1.MTQ, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootCreateAction(mtqsResource, mTQ), &v1alpha1.MTQ{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.MTQ), err
}

// Update takes the representation of a mTQ and updates it. Returns the server's representation of the mTQ, and an error, if there is any.
func (c *FakeMTQs) Update(ctx context.Context, mTQ *v1alpha1.MTQ, opts v1.UpdateOptions) (result *v1alpha1.MTQ, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateAction(mtqsResource, mTQ), &v1alpha1.MTQ{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.MTQ), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeMTQs) UpdateStatus(ctx context.Context, mTQ *v1alpha1.MTQ, opts v1.UpdateOptions) (*v1alpha1.MTQ, error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateSubresourceAction(mtqsResource, "status", mTQ), &v1alpha1.MTQ{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.MTQ), err
}

// Delete takes name of the mTQ and deletes it. Returns an error if one occurs.
func (c *FakeMTQs) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewRootDeleteActionWithOptions(mtqsResource, name, opts), &v1alpha1.MTQ{})
	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeMTQs) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewRootDeleteCollectionAction(mtqsResource, listOpts)

	_, err := c.Fake.Invokes(action, &v1alpha1.MTQList{})
	return err
}

// Patch applies the patch and returns the patched mTQ.
func (c *FakeMTQs) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.MTQ, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootPatchSubresourceAction(mtqsResource, name, pt, data, subresources...), &v1alpha1.MTQ{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.MTQ), err
}
