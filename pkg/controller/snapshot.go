package controller

import (
	"errors"
	"fmt"

	tapi "github.com/k8sdb/apimachinery/api"
	amc "github.com/k8sdb/apimachinery/pkg/controller"
	"github.com/k8sdb/apimachinery/pkg/docker"
	"github.com/k8sdb/apimachinery/pkg/storage"
	amv "github.com/k8sdb/apimachinery/pkg/validator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	apiv1 "k8s.io/client-go/pkg/api/v1"
	batch "k8s.io/client-go/pkg/apis/batch/v1"
)

const (
	SnapshotProcess_Backup  = "backup"
	snapshotType_DumpBackup = "dump-backup"
)

func (c *Controller) ValidateSnapshot(snapshot *tapi.Snapshot) error {
	// Database name can't empty
	databaseName := snapshot.Spec.DatabaseName
	if databaseName == "" {
		return fmt.Errorf(`Object 'DatabaseName' is missing in '%v'`, snapshot.Spec)
	}

	labelMap := map[string]string{
		amc.LabelDatabaseKind:   tapi.ResourceKindPostgres,
		amc.LabelDatabaseName:   snapshot.Spec.DatabaseName,
		amc.LabelSnapshotStatus: string(tapi.SnapshotPhaseRunning),
	}

	snapshotList, err := c.ExtClient.Snapshots(snapshot.Namespace).List(metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labelMap).String(),
	})
	if err != nil {
		return err
	}

	if len(snapshotList.Items) > 0 {
		if snapshot, err = c.ExtClient.Snapshots(snapshot.Namespace).Get(snapshot.Name); err != nil {
			return err
		}

		t := metav1.Now()
		snapshot.Status.StartTime = &t
		snapshot.Status.CompletionTime = &t
		snapshot.Status.Phase = tapi.SnapshotPhaseFailed
		snapshot.Status.Reason = "One Snapshot is already Running"
		if _, err := c.ExtClient.Snapshots(snapshot.Namespace).Update(snapshot); err != nil {
			return err
		}
		return errors.New("One Snapshot is already Running")
	}

	return amv.ValidateSnapshot(c.Client, snapshot)
}

func (c *Controller) GetDatabase(snapshot *tapi.Snapshot) (runtime.Object, error) {
	return c.ExtClient.Postgreses(snapshot.Namespace).Get(snapshot.Spec.DatabaseName)
}

func (c *Controller) GetSnapshotter(snapshot *tapi.Snapshot) (*batch.Job, error) {
	databaseName := snapshot.Spec.DatabaseName
	jobName := snapshot.Name
	jobLabel := map[string]string{
		amc.LabelDatabaseName: databaseName,
		amc.LabelJobType:      SnapshotProcess_Backup,
	}
	backupSpec := snapshot.Spec.SnapshotStorageSpec
	bucket, err := storage.GetContainer(backupSpec)
	if err != nil {
		return nil, err
	}
	postgres, err := c.ExtClient.Postgreses(snapshot.Namespace).Get(databaseName)
	if err != nil {
		return nil, err
	}

	// Get PersistentVolume object for Backup Util pod.
	persistentVolume, err := c.getVolumeForSnapshot(postgres.Spec.Storage, jobName, snapshot.Namespace)
	if err != nil {
		return nil, err
	}

	// Folder name inside Cloud bucket where backup will be uploaded
	folderName := fmt.Sprintf("%v/%v/%v", amc.DatabaseNamePrefix, snapshot.Namespace, snapshot.Spec.DatabaseName)

	job := &batch.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:   jobName,
			Labels: jobLabel,
		},
		Spec: batch.JobSpec{
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: jobLabel,
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Name:  SnapshotProcess_Backup,
							Image: fmt.Sprintf("%s:%s-util", docker.ImagePostgres, postgres.Spec.Version),
							Args: []string{
								fmt.Sprintf(`--process=%s`, SnapshotProcess_Backup),
								fmt.Sprintf(`--host=%s`, databaseName),
								fmt.Sprintf(`--bucket=%s`, bucket),
								fmt.Sprintf(`--folder=%s`, folderName),
								fmt.Sprintf(`--snapshot=%s`, snapshot.Name),
							},
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      "secret",
									MountPath: "/srv/" + tapi.ResourceNamePostgres + "/secrets",
								},
								{
									Name:      persistentVolume.Name,
									MountPath: "/var/" + snapshotType_DumpBackup + "/",
								},
								{
									Name:      "osmconfig",
									ReadOnly:  true,
									MountPath: storage.SecretMountPath,
								},
							},
						},
					},
					Volumes: []apiv1.Volume{
						{
							Name: "secret",
							VolumeSource: apiv1.VolumeSource{
								Secret: &apiv1.SecretVolumeSource{
									SecretName: postgres.Spec.DatabaseSecret.SecretName,
								},
							},
						},
						{
							Name:         persistentVolume.Name,
							VolumeSource: persistentVolume.VolumeSource,
						},
						{
							Name: "osmconfig",
							VolumeSource: apiv1.VolumeSource{
								Secret: &apiv1.SecretVolumeSource{
									SecretName: snapshot.Name,
								},
							},
						},
					},
					RestartPolicy: apiv1.RestartPolicyNever,
				},
			},
		},
	}
	if snapshot.Spec.SnapshotStorageSpec.Local != nil {
		job.Spec.Template.Spec.Containers[0].VolumeMounts = append(job.Spec.Template.Spec.Containers[0].VolumeMounts, apiv1.VolumeMount{
			Name:      snapshot.Spec.SnapshotStorageSpec.Local.Volume.Name,
			MountPath: snapshot.Spec.SnapshotStorageSpec.Local.Path,
		})
		job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, snapshot.Spec.SnapshotStorageSpec.Local.Volume)
	}
	return job, nil
}

func (c *Controller) WipeOutSnapshot(snapshot *tapi.Snapshot) error {
	return c.DeleteSnapshotData(snapshot)
}

func (c *Controller) getVolumeForSnapshot(storage *tapi.StorageSpec, jobName, namespace string) (*apiv1.Volume, error) {
	volume := &apiv1.Volume{
		Name: "util-volume",
	}
	if storage != nil {
		claim := &apiv1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobName,
				Namespace: namespace,
				Annotations: map[string]string{
					"volume.beta.kubernetes.io/storage-class": storage.Class,
				},
			},
			Spec: storage.PersistentVolumeClaimSpec,
		}

		if _, err := c.Client.CoreV1().PersistentVolumeClaims(claim.Namespace).Create(claim); err != nil {
			return nil, err
		}

		volume.PersistentVolumeClaim = &apiv1.PersistentVolumeClaimVolumeSource{
			ClaimName: claim.Name,
		}
	} else {
		volume.EmptyDir = &apiv1.EmptyDirVolumeSource{}
	}
	return volume, nil
}
