package controller

import (
	"fmt"

	"github.com/appscode/log"
	tapi "github.com/k8sdb/apimachinery/api"
	"github.com/k8sdb/apimachinery/pkg/docker"
	"github.com/k8sdb/apimachinery/pkg/storage"
	amv "github.com/k8sdb/apimachinery/pkg/validator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	if _, err := c.ExtClient.Postgreses(snapshot.Namespace).Get(databaseName); err != nil {
		return err
	}

	return amv.ValidateSnapshotSpec(c.Client, snapshot.Spec.SnapshotStorageSpec, snapshot.Namespace)
}

func (c *Controller) GetDatabase(snapshot *tapi.Snapshot) (runtime.Object, error) {
	postgres, err := c.ExtClient.Postgreses(snapshot.Namespace).Get(snapshot.Spec.DatabaseName)
	if err != nil {
		return nil, err
	}

	return postgres, nil
}

func (c *Controller) GetSnapshotter(snapshot *tapi.Snapshot) (*batch.Job, error) {
	databaseName := snapshot.Spec.DatabaseName
	jobName := snapshot.OffshootName()
	jobLabel := map[string]string{
		tapi.LabelDatabaseName: databaseName,
		tapi.LabelJobType:      SnapshotProcess_Backup,
	}
	backupSpec := snapshot.Spec.SnapshotStorageSpec
	bucket, err := backupSpec.Container()
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
	folderName, _ := snapshot.Location()

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
							Resources: snapshot.Spec.Resources,
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
			Name:      "local",
			MountPath: snapshot.Spec.SnapshotStorageSpec.Local.Path,
		})
		job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, apiv1.Volume{
			Name:         "local",
			VolumeSource: snapshot.Spec.SnapshotStorageSpec.Local.VolumeSource,
		})
	}
	return job, nil
}

func (c *Controller) WipeOutSnapshot(snapshot *tapi.Snapshot) error {
	return c.DeleteSnapshotData(snapshot)
}

func (c *Controller) getVolumeForSnapshot(pvcSpec *apiv1.PersistentVolumeClaimSpec, jobName, namespace string) (*apiv1.Volume, error) {
	volume := &apiv1.Volume{
		Name: "util-volume",
	}
	if pvcSpec != nil {
		if len(pvcSpec.AccessModes) == 0 {
			pvcSpec.AccessModes = []apiv1.PersistentVolumeAccessMode{
				apiv1.ReadWriteOnce,
			}
			log.Infof(`Using "%v" as AccessModes in "%v"`, apiv1.ReadWriteOnce, *pvcSpec)
		}

		claim := &apiv1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobName,
				Namespace: namespace,
				Annotations: map[string]string{
					"volume.beta.kubernetes.io/storage-class": *pvcSpec.StorageClassName,
				},
			},
			Spec: *pvcSpec,
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
