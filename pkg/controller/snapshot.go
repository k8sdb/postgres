package controller

import (
	"fmt"

	api "github.com/kubedb/apimachinery/apis/kubedb/v1alpha1"
	"github.com/kubedb/apimachinery/pkg/docker"
	amv "github.com/kubedb/apimachinery/pkg/validator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (c *Controller) ValidateSnapshot(snapshot *api.Snapshot) error {
	// Database name can't empty
	databaseName := snapshot.Spec.DatabaseName
	if databaseName == "" {
		return fmt.Errorf(`object 'DatabaseName' is missing in '%v'`, snapshot.Spec)
	}

	postgres, err := c.extClient.Postgreses(snapshot.Namespace).Get(databaseName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if err := docker.CheckDockerImageVersion(c.opt.Docker.GetToolsImage(postgres), string(postgres.Spec.Version)); err != nil {
		return fmt.Errorf(`image %s not found`, c.opt.Docker.GetToolsImageWithTag(postgres))
	}

	return amv.ValidateSnapshotSpec(c.client, snapshot.Spec.SnapshotStorageSpec, snapshot.Namespace)
}

func (c *Controller) GetDatabase(snapshot *api.Snapshot) (runtime.Object, error) {
	postgres, err := c.extClient.Postgreses(snapshot.Namespace).Get(snapshot.Spec.DatabaseName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return postgres, nil
}

func (c *Controller) WipeOutSnapshot(snapshot *api.Snapshot) error {
	if snapshot.Spec.Local != nil {
		local := snapshot.Spec.Local
		if local.VolumeSource.EmptyDir != nil {
			return nil
		}
	}
	return c.DeleteSnapshotData(snapshot)
}
