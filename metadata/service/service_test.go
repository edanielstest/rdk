package service_test

import (
	"context"
	"testing"

	"go.viam.com/test"

	"go.viam.com/core/component/arm"
	"go.viam.com/core/metadata/service"
	"go.viam.com/core/resource"
)

func TestAdd(t *testing.T) {
	r, err := service.New()
	test.That(t, err, test.ShouldBeNil)
	service := r.All()[0]
	arm := arm.Named("arm1")
	test.That(t, err, test.ShouldBeNil)
	sensor := resource.NewName(
		resource.ResourceNamespaceCore,
		resource.ResourceTypeComponent,
		resource.ResourceSubtypeSensor,
		"sensor1",
	)
	test.That(t, err, test.ShouldBeNil)

	newMetadata := resource.NewFromSubtype(service.Subtype, "metadata1")

	test.That(t, err, test.ShouldBeNil)

	for _, tc := range []struct {
		Name        string
		NewResource resource.Name
		Expected    []resource.Name
		Err         string
	}{
		{
			"invalid addition",
			resource.Name{},
			nil,
			"uuid field for resource missing or invalid",
		},
		{
			"add metadata",
			newMetadata,
			[]resource.Name{service, newMetadata},
			"",
		},
		{
			"one addition",
			arm,
			[]resource.Name{service, newMetadata, arm},
			"",
		},
		{
			"duplicate addition",
			arm,
			nil,
			"already exists",
		},
		{
			"another addition",
			sensor,
			[]resource.Name{service, newMetadata, arm, sensor},
			"",
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			err := r.Add(tc.NewResource)
			if tc.Err == "" {
				test.That(t, err, test.ShouldBeNil)
				test.That(t, r.All(), test.ShouldResemble, tc.Expected)
			} else {
				test.That(t, err, test.ShouldNotBeNil)
				test.That(t, err.Error(), test.ShouldContainSubstring, tc.Err)
			}
		})
	}
}

func TestReplace(t *testing.T) {
	r, err := service.New()
	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(r.All()), test.ShouldEqual, 1)
	arm := arm.Named("arm1")
	test.That(t, err, test.ShouldBeNil)
	sensor := resource.NewName(
		resource.ResourceNamespaceCore,
		resource.ResourceTypeComponent,
		resource.ResourceSubtypeSensor,
		"sensor1",
	)
	test.That(t, err, test.ShouldBeNil)

	metadataSvc := resource.NewFromSubtype(service.Subtype, "")
	test.That(t, err, test.ShouldBeNil)

	for _, tc := range []struct {
		Name         string
		NewResources []resource.Name
		Err          string
	}{
		{
			"invalid replacement",
			[]resource.Name{{}},
			"uuid field for resource missing or invalid",
		},
		{
			"replace",
			[]resource.Name{metadataSvc, arm, sensor},
			"",
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			err := r.Replace(tc.NewResources)
			if tc.Err == "" {
				test.That(t, err, test.ShouldBeNil)
				test.That(t, r.All(), test.ShouldResemble, tc.NewResources)
			} else {
				test.That(t, err, test.ShouldNotBeNil)
				test.That(t, err.Error(), test.ShouldContainSubstring, tc.Err)
			}
		})
	}
}

func TestContextService(t *testing.T) {
	ctx := context.Background()
	svc, err := service.New()
	test.That(t, err, test.ShouldBeNil)
	ctx = service.ContextWithService(ctx, svc)
	svc2 := service.ContextService(context.Background())
	test.That(t, svc2, test.ShouldBeNil)
	svc2 = service.ContextService(ctx)
	test.That(t, svc2, test.ShouldEqual, svc)
}