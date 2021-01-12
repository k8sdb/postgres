/*
Copyright AppsCode Inc. and Contributors

Licensed under the AppsCode Community License 1.0.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://github.com/appscode/licenses/raw/1.0.0/AppsCode-Community-1.0.0.md

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"gomodules.xyz/version"

	"github.com/appscode/go/types"
	"gomodules.xyz/pointer"
	"gomodules.xyz/x/log"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kutil "kmodules.xyz/client-go"
	kmapi "kmodules.xyz/client-go/api/v1"
	app_util "kmodules.xyz/client-go/apps/v1"
	core_util "kmodules.xyz/client-go/core/v1"
	meta_util "kmodules.xyz/client-go/meta"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	catalog "kubedb.dev/apimachinery/apis/catalog/v1alpha1"
	api "kubedb.dev/apimachinery/apis/kubedb/v1alpha2"
	"kubedb.dev/apimachinery/pkg/eventer"
)

const (
	LeaderElectionImage        = "hremon331046/leadertest:11"
	PostgresInitContainerName  = "postgres-init-container"
	PostgresInitContainerImage = "hremon331046/postgres-init-container:latest"

	sharedTlsVolumeMountPath = "/tls/certs"
	clientTlsVolumeMountPath = "/certs/client"
	serverTlsVolumeMountPath = "/certs/server"

	serverTlsVolumeName   = "tls-volume-server"
	clientTlsVolumeName   = "tls-volume-client"
	leaderTlsVolumeName   = "leader-elector-tls-volume"
	sharedTlsVolumeName   = "certs"
	exporterTlsVolumename = "exporter-tls-volume"
)

func getMajorPgVersion(postgres *api.Postgres) (int64, error) {
	ver, err := version.NewVersion(postgres.Spec.Version)
	if err != nil {
		//TODO
		log.Error(err)
		return 0, err
	}
	return ver.Major(), nil
}

func (c *Controller) ensureStatefulSet(
	db *api.Postgres,
	postgresVersion *catalog.PostgresVersion,
	envList []core.EnvVar,
) (kutil.VerbType, error) {

	if err := c.checkStatefulSet(db); err != nil {
		return kutil.VerbUnchanged, err
	}

	statefulSetMeta := metav1.ObjectMeta{
		Name:      db.OffshootName(),
		Namespace: db.Namespace,
	}

	owner := metav1.NewControllerRef(db, api.SchemeGroupVersion.WithKind(api.ResourceKindPostgres))

	replicas := int32(1)
	if db.Spec.Replicas != nil {
		replicas = pointer.Int32(db.Spec.Replicas)
	}

	statefulSet, vt, err := app_util.CreateOrPatchStatefulSet(
		context.TODO(),
		c.Client,
		statefulSetMeta,
		func(in *apps.StatefulSet) *apps.StatefulSet {
			in.Labels = db.OffshootLabels()
			in.Annotations = db.Spec.PodTemplate.Controller.Annotations
			core_util.EnsureOwnerReference(&in.ObjectMeta, owner)

			in.Spec.Replicas = pointer.Int32P(replicas)

			in.Spec.ServiceName = db.GoverningServiceName()
			in.Spec.Selector = &metav1.LabelSelector{
				MatchLabels: db.OffshootSelectors(),
			}
			in.Spec.Template.Labels = db.OffshootSelectors()
			in.Spec.Template.Annotations = db.Spec.PodTemplate.Annotations
			in.Spec.Template.Spec.InitContainers = core_util.UpsertContainers(in.Spec.Template.Spec.InitContainers, db.Spec.PodTemplate.Spec.InitContainers)
			in.Spec.Template.Spec.InitContainers = getInitContainers(in, db)

			in.Spec.Template.Spec.Containers = getContainers(in, db, postgresVersion)

			in = upsertEnv(in, db, envList)
			in = upsertUserEnv(in, db)
			in = upsertPort(in)

			in.Spec.Template.Spec.NodeSelector = db.Spec.PodTemplate.Spec.NodeSelector
			in.Spec.Template.Spec.Affinity = db.Spec.PodTemplate.Spec.Affinity
			if db.Spec.PodTemplate.Spec.SchedulerName != "" {
				in.Spec.Template.Spec.SchedulerName = db.Spec.PodTemplate.Spec.SchedulerName
			}
			in.Spec.Template.Spec.Tolerations = db.Spec.PodTemplate.Spec.Tolerations
			in.Spec.Template.Spec.ImagePullSecrets = db.Spec.PodTemplate.Spec.ImagePullSecrets
			in.Spec.Template.Spec.PriorityClassName = db.Spec.PodTemplate.Spec.PriorityClassName
			in.Spec.Template.Spec.Priority = db.Spec.PodTemplate.Spec.Priority
			in.Spec.Template.Spec.SecurityContext = db.Spec.PodTemplate.Spec.SecurityContext

			in = c.upsertMonitoringContainer(in, db, postgresVersion)
			if db.Spec.Archiver != nil {
				if db.Spec.Archiver.Storage != nil {
					//Creating secret for cloud providers
					archiverStorage := db.Spec.Archiver.Storage
					if archiverStorage.Local == nil {
						in = upsertArchiveSecret(in, archiverStorage.StorageSecretName)
					}
				}
			}

			if !kmapi.HasCondition(db.Status.Conditions, api.DatabaseDataRestored) {
				initSource := db.Spec.Init
				if initSource != nil && initSource.PostgresWAL != nil && initSource.PostgresWAL.Local == nil {
					//Getting secret for cloud providers
					in = upsertInitWalSecret(in, db.Spec.Init.PostgresWAL.StorageSecretName)
				}
				if initSource != nil && initSource.Script != nil {
					in = upsertInitScript(in, db.Spec.Init.Script.VolumeSource)
				}
			}

			in = upsertShm(in)
			in = upsertDataVolume(in, db)
			in = upsertCustomConfig(in, db)
			in = upsertSharedScriptsVolume(in, db)
			if db.Spec.TLS != nil {
				in = upsertTLSVolume(in, db)
				in = upsertCertficatesVolume(in, db)

			}

			in.Spec.Template.Spec.ServiceAccountName = db.Spec.PodTemplate.Spec.ServiceAccountName
			in.Spec.UpdateStrategy = apps.StatefulSetUpdateStrategy{
				Type: apps.OnDeleteStatefulSetStrategyType,
			}

			return in
		},
		metav1.PatchOptions{},
	)

	if err != nil {
		return kutil.VerbUnchanged, err
	}

	if vt == kutil.VerbCreated || vt == kutil.VerbPatched {
		// Check StatefulSet Pod status
		if err := c.CheckStatefulSetPodStatus(statefulSet); err != nil {
			return kutil.VerbUnchanged, err
		}

		c.Recorder.Eventf(
			db,
			core.EventTypeNormal,
			eventer.EventReasonSuccessful,
			"Successfully %v StatefulSet",
			vt,
		)
	}

	// ensure pdb
	if err := c.CreateStatefulSetPodDisruptionBudget(statefulSet); err != nil {
		return vt, err
	}
	return vt, nil
}

func (c *Controller) CheckStatefulSetPodStatus(statefulSet *apps.StatefulSet) error {
	err := core_util.WaitUntilPodRunningBySelector(
		context.TODO(),
		c.Client,
		statefulSet.Namespace,
		statefulSet.Spec.Selector,
		int(pointer.Int32(statefulSet.Spec.Replicas)),
	)
	if err != nil {
		return err
	}
	return nil
}

func (c *Controller) ensureCombinedNode(db *api.Postgres, postgresVersion *catalog.PostgresVersion) (kutil.VerbType, error) {
	standbyMode := api.WarmPostgresStandbyMode
	streamingMode := api.AsynchronousPostgresStreamingMode

	if db.Spec.StandbyMode != nil {
		standbyMode = *db.Spec.StandbyMode
	}
	if db.Spec.StreamingMode != nil {
		streamingMode = *db.Spec.StreamingMode
	}

	envList := []core.EnvVar{
		{
			Name:  "STANDBY",
			Value: strings.ToLower(string(standbyMode)),
		},
		{
			Name:  "STREAMING",
			Value: strings.ToLower(string(streamingMode)),
		},
	}

	if db.Spec.Archiver != nil {
		archiverStorage := db.Spec.Archiver.Storage
		if archiverStorage != nil {
			envList = append(envList,
				core.EnvVar{
					Name:  "ARCHIVE",
					Value: "wal-g",
				},
			)
			if archiverStorage.S3 != nil {
				envList = append(envList,
					core.EnvVar{
						Name:  "ARCHIVE_S3_PREFIX",
						Value: fmt.Sprintf("s3://%v/%v", archiverStorage.S3.Bucket, WalDataDir(db)),
					},
				)
				if archiverStorage.S3.Endpoint != "" && !strings.HasSuffix(archiverStorage.S3.Endpoint, ".amazonaws.com") {
					//means it is a  compatible storage
					envList = append(envList,
						core.EnvVar{
							Name:  "ARCHIVE_S3_ENDPOINT",
							Value: archiverStorage.S3.Endpoint,
						},
					)
				}
				if archiverStorage.S3.Region != "" {
					envList = append(envList,
						core.EnvVar{
							Name:  "ARCHIVE_S3_REGION",
							Value: archiverStorage.S3.Region,
						},
					)
				}
			} else if archiverStorage.GCS != nil {
				envList = append(envList,
					core.EnvVar{
						Name:  "ARCHIVE_GS_PREFIX",
						Value: fmt.Sprintf("gs://%v/%v", archiverStorage.GCS.Bucket, WalDataDir(db)),
					},
				)
			} else if archiverStorage.Azure != nil {
				envList = append(envList,
					core.EnvVar{
						Name:  "ARCHIVE_AZ_PREFIX",
						Value: fmt.Sprintf("azure://%v/%v", archiverStorage.Azure.Container, WalDataDir(db)),
					},
				)
			} else if archiverStorage.Swift != nil {
				envList = append(envList,
					core.EnvVar{
						Name:  "ARCHIVE_SWIFT_PREFIX",
						Value: fmt.Sprintf("swift://%v/%v", archiverStorage.Swift.Container, WalDataDir(db)),
					},
				)
			} else if archiverStorage.Local != nil {
				envList = append(envList,
					core.EnvVar{
						Name:  "ARCHIVE_FILE_PREFIX",
						Value: archiverStorage.Local.MountPath,
					},
				)
			}
		}
	}

	if db.Spec.Init != nil {
		wal := db.Spec.Init.PostgresWAL
		if wal != nil {
			envList = append(envList, walRecoveryConfig(wal)...)
		}
	}

	return c.ensureStatefulSet(db, postgresVersion, envList)
}

func (c *Controller) checkStatefulSet(db *api.Postgres) error {
	name := db.OffshootName()
	// SatatefulSet for Postgres database
	statefulSet, err := c.Client.AppsV1().StatefulSets(db.Namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		if kerr.IsNotFound(err) {
			return nil
		} else {
			return err
		}
	}

	if statefulSet.Labels[meta_util.NameLabelKey] != db.ResourceFQN() ||
		statefulSet.Labels[meta_util.InstanceLabelKey] != name {
		return fmt.Errorf(`intended statefulSet "%v/%v" already exists`, db.Namespace, name)
	}

	return nil
}

func upsertEnv(statefulSet *apps.StatefulSet, db *api.Postgres, envs []core.EnvVar) *apps.StatefulSet {
	majorPGVersion, err := getMajorPgVersion(db)
	if err != nil {
		log.Error("couldn't get version's major part")
	}

	envList := []core.EnvVar{
		{
			Name: "NAMESPACE",
			ValueFrom: &core.EnvVarSource{
				FieldRef: &core.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
		{
			Name:  "PRIMARY_HOST",
			Value: db.ServiceName(),
		},
		{
			Name: EnvPostgresUser,
			ValueFrom: &core.EnvVarSource{
				SecretKeyRef: &core.SecretKeySelector{
					LocalObjectReference: core.LocalObjectReference{
						Name: db.Spec.AuthSecret.Name,
					},
					Key: core.BasicAuthUsernameKey,
				},
			},
		},
		{
			Name: EnvPostgresPassword,
			ValueFrom: &core.EnvVarSource{
				SecretKeyRef: &core.SecretKeySelector{
					LocalObjectReference: core.LocalObjectReference{
						Name: db.Spec.AuthSecret.Name,
					},
					Key: core.BasicAuthPasswordKey,
				},
			},
		},
		{
			Name:  "PG_VERSION",
			Value: db.Spec.Version,
		},
		{
			Name:  "MAJOR_PG_VERSION",
			Value: strconv.Itoa(int(majorPGVersion)),
		},
	}

	envList = append(envList, envs...)

	if db.Spec.TLS != nil {
		tlEnv := []core.EnvVar{
			{
				Name:  "SSL_MODE",
				Value: "ON",
			},
		}
		envList = append(envList, tlEnv...)
	}

	// To do this, Upsert Container first
	for i, container := range statefulSet.Spec.Template.Spec.Containers {
		if container.Name == api.ResourceSingularPostgres || container.Name == api.PostgresLeaderElectionContainerName {
			statefulSet.Spec.Template.Spec.Containers[i].Env = core_util.UpsertEnvVars(container.Env, envList...)
		}
	}
	for i, initContainer := range statefulSet.Spec.Template.Spec.InitContainers {
		if initContainer.Name == PostgresInitContainerName {
			statefulSet.Spec.Template.Spec.InitContainers[i].Env = core_util.UpsertEnvVars(initContainer.Env, envList...)
		}
	}
	return statefulSet
}

// upsertUserEnv add/overwrite env from user provided env in crd spec
func upsertUserEnv(statefulSet *apps.StatefulSet, postgress *api.Postgres) *apps.StatefulSet {
	for i, container := range statefulSet.Spec.Template.Spec.Containers {
		if container.Name == api.ResourceSingularPostgres {
			statefulSet.Spec.Template.Spec.Containers[i].Env = core_util.UpsertEnvVars(container.Env, postgress.Spec.PodTemplate.Spec.Env...)
			return statefulSet
		}
	}
	return statefulSet
}
func upsertPort(statefulSet *apps.StatefulSet) *apps.StatefulSet {
	getPostgresPorts := func() []core.ContainerPort {
		portList := []core.ContainerPort{
			{
				Name:          api.PostgresDatabasePortName,
				ContainerPort: api.PostgresDatabasePort,
				Protocol:      core.ProtocolTCP,
			},
		}
		return portList
	}
	getLeaderPorts := func() []core.ContainerPort {
		portList := []core.ContainerPort{
			{
				Name:          api.PostgresLeaderElectionPortName,
				ContainerPort: api.PostgresLeaderElectionPort,
				Protocol:      core.ProtocolTCP,
			},
			{
				Name:          api.PostgresLeaderElectionClientPortName,
				ContainerPort: api.PostgresLeaderElectionClientPort,
				Protocol:      core.ProtocolTCP,
			},
		}
		return portList
	}

	for i, container := range statefulSet.Spec.Template.Spec.Containers {
		if container.Name == api.ResourceSingularPostgres {
			statefulSet.Spec.Template.Spec.Containers[i].Ports = getPostgresPorts()
		} else if container.Name == api.PostgresLeaderElectionContainerName {
			statefulSet.Spec.Template.Spec.Containers[i].Ports = getLeaderPorts()
		}
	}

	return statefulSet
}

func (c *Controller) upsertMonitoringContainer(statefulSet *apps.StatefulSet, db *api.Postgres, postgresVersion *catalog.PostgresVersion) *apps.StatefulSet {
	if db.Spec.Monitor != nil && db.Spec.Monitor.Agent.Vendor() == mona.VendorPrometheus {
		container := core.Container{
			Name: "exporter",
			Args: append([]string{
				"--log.level=info",
			}, db.Spec.Monitor.Prometheus.Exporter.Args...),
			Image:           postgresVersion.Spec.Exporter.Image,
			ImagePullPolicy: core.PullIfNotPresent,
			Ports: []core.ContainerPort{
				{
					Name:          mona.PrometheusExporterPortName,
					Protocol:      core.ProtocolTCP,
					ContainerPort: int32(db.Spec.Monitor.Prometheus.Exporter.Port),
				},
			},
			Env:             db.Spec.Monitor.Prometheus.Exporter.Env,
			Resources:       db.Spec.Monitor.Prometheus.Exporter.Resources,
			SecurityContext: db.Spec.Monitor.Prometheus.Exporter.SecurityContext,
		}

		envList := []core.EnvVar{
			{
				Name:  "DATA_SOURCE_URI",
				Value: fmt.Sprintf("localhost:%d/?sslmode=disable", api.PostgresDatabasePort),
			},
			{
				Name: "DATA_SOURCE_USER",
				ValueFrom: &core.EnvVarSource{
					SecretKeyRef: &core.SecretKeySelector{
						LocalObjectReference: core.LocalObjectReference{
							Name: db.Spec.AuthSecret.Name,
						},
						Key: core.BasicAuthUsernameKey,
					},
				},
			},
			{
				Name: "DATA_SOURCE_PASS",
				ValueFrom: &core.EnvVarSource{
					SecretKeyRef: &core.SecretKeySelector{
						LocalObjectReference: core.LocalObjectReference{
							Name: db.Spec.AuthSecret.Name,
						},
						Key: core.BasicAuthPasswordKey,
					},
				},
			},
			{
				Name:  "PG_EXPORTER_WEB_LISTEN_ADDRESS",
				Value: fmt.Sprintf(":%d", db.Spec.Monitor.Prometheus.Exporter.Port),
			},
			{
				Name:  "PG_EXPORTER_WEB_TELEMETRY_PATH",
				Value: db.StatsService().Path(),
			},
		}

		container.Env = core_util.UpsertEnvVars(container.Env, envList...)
		containers := statefulSet.Spec.Template.Spec.Containers
		containers = core_util.UpsertContainer(containers, container)
		statefulSet.Spec.Template.Spec.Containers = containers
	}
	return statefulSet
}

func upsertArchiveSecret(statefulSet *apps.StatefulSet, secretName string) *apps.StatefulSet {
	for i, container := range statefulSet.Spec.Template.Spec.Containers {
		if container.Name == api.ResourceSingularPostgres {
			volumeMount := core.VolumeMount{
				Name:      "wal-g-archive",
				MountPath: "/srv/wal-g/archive/secrets",
			}
			volumeMounts := container.VolumeMounts
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, volumeMount)
			statefulSet.Spec.Template.Spec.Containers[i].VolumeMounts = volumeMounts

			volume := core.Volume{
				Name: "wal-g-archive",
				VolumeSource: core.VolumeSource{
					Secret: &core.SecretVolumeSource{
						SecretName: secretName,
					},
				},
			}
			volumes := statefulSet.Spec.Template.Spec.Volumes
			volumes = core_util.UpsertVolume(volumes, volume)
			statefulSet.Spec.Template.Spec.Volumes = volumes
			return statefulSet
		}
	}
	return statefulSet
}

func upsertInitWalSecret(statefulSet *apps.StatefulSet, secretName string) *apps.StatefulSet {
	for i, container := range statefulSet.Spec.Template.Spec.Containers {
		if container.Name == api.ResourceSingularPostgres {
			volumeMount := core.VolumeMount{
				Name:      "wal-g-restore",
				MountPath: "/srv/wal-g/restore/secrets",
			}
			volumeMounts := container.VolumeMounts
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, volumeMount)
			statefulSet.Spec.Template.Spec.Containers[i].VolumeMounts = volumeMounts

			volume := core.Volume{
				Name: "wal-g-restore",
				VolumeSource: core.VolumeSource{
					Secret: &core.SecretVolumeSource{
						SecretName: secretName,
					},
				},
			}
			volumes := statefulSet.Spec.Template.Spec.Volumes
			volumes = core_util.UpsertVolume(volumes, volume)
			statefulSet.Spec.Template.Spec.Volumes = volumes
			return statefulSet
		}
	}
	return statefulSet
}

func upsertShm(statefulSet *apps.StatefulSet) *apps.StatefulSet {
	for i, container := range statefulSet.Spec.Template.Spec.Containers {
		if container.Name == api.ResourceSingularPostgres {
			volumeMount := core.VolumeMount{
				Name:      "shared-memory",
				MountPath: "/dev/shm",
			}
			volumeMounts := container.VolumeMounts
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, volumeMount)
			statefulSet.Spec.Template.Spec.Containers[i].VolumeMounts = volumeMounts

			configVolume := core.Volume{
				Name: "shared-memory",
				VolumeSource: core.VolumeSource{
					EmptyDir: &core.EmptyDirVolumeSource{
						Medium: core.StorageMediumMemory,
					},
				},
			}
			volumes := statefulSet.Spec.Template.Spec.Volumes
			volumes = core_util.UpsertVolume(volumes, configVolume)
			statefulSet.Spec.Template.Spec.Volumes = volumes
			return statefulSet
		}
	}
	return statefulSet
}

func upsertInitScript(statefulSet *apps.StatefulSet, script core.VolumeSource) *apps.StatefulSet {
	for i, container := range statefulSet.Spec.Template.Spec.Containers {
		if container.Name == api.ResourceSingularPostgres {
			volumeMount := core.VolumeMount{
				Name:      "initial-script",
				MountPath: "/var/initdb",
			}
			volumeMounts := container.VolumeMounts
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, volumeMount)
			statefulSet.Spec.Template.Spec.Containers[i].VolumeMounts = volumeMounts

			volume := core.Volume{
				Name:         "initial-script",
				VolumeSource: script,
			}
			volumes := statefulSet.Spec.Template.Spec.Volumes
			volumes = core_util.UpsertVolume(volumes, volume)
			statefulSet.Spec.Template.Spec.Volumes = volumes
			return statefulSet
		}
	}
	return statefulSet
}

func upsertDataVolume(statefulSet *apps.StatefulSet, db *api.Postgres) *apps.StatefulSet {
	if db.Spec.Archiver != nil || db.Spec.Init != nil {
		// Add a PV
		if db.Spec.Archiver != nil &&
			db.Spec.Archiver.Storage != nil &&
			db.Spec.Archiver.Storage.Local != nil {
			pgLocalVol := db.Spec.Archiver.Storage.Local
			podSpec := statefulSet.Spec.Template.Spec
			if pgLocalVol != nil {
				volume := core.Volume{
					Name:         "local-archive",
					VolumeSource: pgLocalVol.VolumeSource,
				}
				statefulSet.Spec.Template.Spec.Volumes = append(podSpec.Volumes, volume)

				statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, core.VolumeMount{
					Name:      "local-archive",
					MountPath: pgLocalVol.MountPath,
					//SubPath:  use of SubPath is discouraged
					// due to the contrasting natures of PV claim and wal-g directory
				})
			}
		}
		if db.Spec.Init != nil &&
			db.Spec.Init.PostgresWAL != nil &&
			db.Spec.Init.PostgresWAL.Local != nil {
			pgLocalVol := db.Spec.Init.PostgresWAL.Local
			podSpec := statefulSet.Spec.Template.Spec
			if pgLocalVol != nil {
				volume := core.Volume{
					Name:         "local-init",
					VolumeSource: pgLocalVol.VolumeSource,
				}
				statefulSet.Spec.Template.Spec.Volumes = append(podSpec.Volumes, volume)

				statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, core.VolumeMount{
					Name:      "local-init",
					MountPath: pgLocalVol.MountPath,
					//SubPath: is used to locate existing archive
					//from given mountPath, therefore isn't mounted.
				})
			}
		}
	}

	for i, container := range statefulSet.Spec.Template.Spec.Containers {
		if container.Name == api.ResourceSingularPostgres || container.Name == api.PostgresLeaderElectionContainerName {
			var volumeMount core.VolumeMount
			if container.Name == api.ResourceSingularPostgres || container.Name == api.PostgresLeaderElectionContainerName {
				volumeMount = core.VolumeMount{
					Name:      "data",
					MountPath: "/var/pv",
				}
			}
			volumeMounts := container.VolumeMounts
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, volumeMount)
			statefulSet.Spec.Template.Spec.Containers[i].VolumeMounts = volumeMounts

			pvcSpec := db.Spec.Storage
			if db.Spec.StorageType == api.StorageTypeEphemeral {
				ed := core.EmptyDirVolumeSource{}
				if pvcSpec != nil {
					if sz, found := pvcSpec.Resources.Requests[core.ResourceStorage]; found {
						ed.SizeLimit = &sz
					}
				}
				statefulSet.Spec.Template.Spec.Volumes = core_util.UpsertVolume(
					statefulSet.Spec.Template.Spec.Volumes,
					core.Volume{
						Name: "data",
						VolumeSource: core.VolumeSource{
							EmptyDir: &ed,
						},
					})
			} else {
				if len(pvcSpec.AccessModes) == 0 {
					pvcSpec.AccessModes = []core.PersistentVolumeAccessMode{
						core.ReadWriteOnce,
					}
					log.Infof(`Using "%v" as AccessModes in postgres.Spec.Storage`, core.ReadWriteOnce)
				}

				claim := core.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name: "data",
					},
					Spec: *pvcSpec,
				}
				if pvcSpec.StorageClassName != nil {
					claim.Annotations = map[string]string{
						"volume.beta.kubernetes.io/storage-class": *pvcSpec.StorageClassName,
					}
				}
				statefulSet.Spec.VolumeClaimTemplates = core_util.UpsertVolumeClaim(statefulSet.Spec.VolumeClaimTemplates, claim)
			}
			//	break
		}
	}
	return statefulSet
}

func upsertCustomConfig(statefulSet *apps.StatefulSet, db *api.Postgres) *apps.StatefulSet {
	if db.Spec.ConfigSecret != nil {
		for i, container := range statefulSet.Spec.Template.Spec.Containers {
			if container.Name == api.ResourceSingularPostgres {
				configVolumeMount := core.VolumeMount{
					Name:      "custom-config",
					MountPath: "/etc/config",
				}
				volumeMounts := container.VolumeMounts
				volumeMounts = core_util.UpsertVolumeMount(volumeMounts, configVolumeMount)
				statefulSet.Spec.Template.Spec.Containers[i].VolumeMounts = volumeMounts

				configVolume := core.Volume{
					Name: "custom-config",
					VolumeSource: core.VolumeSource{
						Secret: &core.SecretVolumeSource{
							SecretName: db.Spec.ConfigSecret.Name,
						},
					},
				}

				volumes := statefulSet.Spec.Template.Spec.Volumes
				volumes = core_util.UpsertVolume(volumes, configVolume)
				statefulSet.Spec.Template.Spec.Volumes = volumes
				break
			}
		}
	}
	return statefulSet
}

func upsertSharedScriptsVolume(statefulSet *apps.StatefulSet, postgres *api.Postgres) *apps.StatefulSet {

	for i, container := range statefulSet.Spec.Template.Spec.Containers {
		if container.Name == api.ResourceSingularPostgres || container.Name == api.PostgresLeaderElectionContainerName {
			configVolumeMount := core.VolumeMount{
				Name:      "scripts",
				MountPath: "/run_scripts",
			}
			volumeMounts := container.VolumeMounts
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, configVolumeMount)
			statefulSet.Spec.Template.Spec.Containers[i].VolumeMounts = volumeMounts

		}
	}
	for i, initContainer := range statefulSet.Spec.Template.Spec.InitContainers {
		if initContainer.Name == PostgresInitContainerName {
			configVolumeMount := core.VolumeMount{
				Name:      "scripts",
				MountPath: "/run_scripts",
			}
			volumeMounts := initContainer.VolumeMounts
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, configVolumeMount)
			statefulSet.Spec.Template.Spec.InitContainers[i].VolumeMounts = volumeMounts

		}
	}

	configVolume := core.Volume{
		Name: "scripts",
		VolumeSource: core.VolumeSource{
			EmptyDir: &core.EmptyDirVolumeSource{},
		},
	}

	volumes := statefulSet.Spec.Template.Spec.Volumes
	volumes = core_util.UpsertVolume(volumes, configVolume)
	statefulSet.Spec.Template.Spec.Volumes = volumes

	return statefulSet
}

func upsertCertficatesVolume(statefulSet *apps.StatefulSet, postgres *api.Postgres) *apps.StatefulSet {

	for i, container := range statefulSet.Spec.Template.Spec.Containers {
		if container.Name == api.ResourceSingularPostgres {
			configVolumeMount := core.VolumeMount{
				Name:      sharedTlsVolumeName,
				MountPath: sharedTlsVolumeMountPath,
			}
			volumeMounts := container.VolumeMounts
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, configVolumeMount)
			statefulSet.Spec.Template.Spec.Containers[i].VolumeMounts = volumeMounts

		}
	}
	for i, initContainer := range statefulSet.Spec.Template.Spec.InitContainers {
		if initContainer.Name == PostgresInitContainerName {
			configVolumeMount := core.VolumeMount{
				Name:      sharedTlsVolumeName,
				MountPath: sharedTlsVolumeMountPath,
			}
			volumeMounts := initContainer.VolumeMounts
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, configVolumeMount)
			statefulSet.Spec.Template.Spec.InitContainers[i].VolumeMounts = volumeMounts

		}
	}

	configVolume := core.Volume{
		Name: sharedTlsVolumeName,
		VolumeSource: core.VolumeSource{
			EmptyDir: &core.EmptyDirVolumeSource{},
		},
	}

	volumes := statefulSet.Spec.Template.Spec.Volumes
	volumes = core_util.UpsertVolume(volumes, configVolume)
	statefulSet.Spec.Template.Spec.Volumes = volumes

	return statefulSet
}
func walRecoveryConfig(wal *api.PostgresWALSourceSpec) []core.EnvVar {
	envList := []core.EnvVar{
		{
			Name:  "RESTORE",
			Value: "true",
		},
	}

	if wal.S3 != nil {
		envList = append(envList,
			core.EnvVar{
				Name:  "RESTORE_S3_PREFIX",
				Value: fmt.Sprintf("s3://%v/%v", wal.S3.Bucket, wal.S3.Prefix),
			},
		)
		if wal.S3.Endpoint != "" && !strings.HasSuffix(wal.S3.Endpoint, ".amazonaws.com") {
			envList = append(envList,
				core.EnvVar{
					Name:  "RESTORE_S3_ENDPOINT",
					Value: wal.S3.Endpoint,
				},
			)
		}
		if wal.S3.Region != "" {
			envList = append(envList,
				core.EnvVar{
					Name:  "RESTORE_S3_REGION",
					Value: wal.S3.Region,
				},
			)
		}
	} else if wal.GCS != nil {
		envList = append(envList,
			core.EnvVar{
				Name:  "RESTORE_GS_PREFIX",
				Value: fmt.Sprintf("gs://%v/%v", wal.GCS.Bucket, wal.GCS.Prefix),
			},
		)
	} else if wal.Azure != nil {
		envList = append(envList,
			core.EnvVar{
				Name:  "RESTORE_AZ_PREFIX",
				Value: fmt.Sprintf("azure://%v/%v", wal.Azure.Container, wal.Azure.Prefix),
			},
		)
	} else if wal.Swift != nil {
		envList = append(envList,
			core.EnvVar{
				Name:  "RESTORE_SWIFT_PREFIX",
				Value: fmt.Sprintf("swift://%v/%v", wal.Swift.Container, wal.Swift.Prefix),
			},
		)
	} else if wal.Local != nil {
		archiveSource := path.Join("/", wal.Local.MountPath, wal.Local.SubPath)
		envList = append(envList,
			core.EnvVar{
				Name:  "RESTORE_FILE_PREFIX",
				Value: archiveSource,
			},
		)
	}

	if wal.PITR != nil {
		envList = append(envList,
			[]core.EnvVar{
				{
					Name:  "PITR",
					Value: "true",
				},
				{
					Name:  "TARGET_INCLUSIVE",
					Value: fmt.Sprintf("%t", *wal.PITR.TargetInclusive),
				},
			}...)
		if wal.PITR.TargetTime != "" {
			envList = append(envList,
				[]core.EnvVar{
					{
						Name:  "TARGET_TIME",
						Value: wal.PITR.TargetTime,
					},
				}...)
		}
		if wal.PITR.TargetTimeline != "" {
			envList = append(envList,
				[]core.EnvVar{
					{
						Name:  "TARGET_TIMELINE",
						Value: wal.PITR.TargetTimeline,
					},
				}...)
		}
		if wal.PITR.TargetXID != "" {
			envList = append(envList,
				[]core.EnvVar{
					{
						Name:  "TARGET_XID",
						Value: wal.PITR.TargetXID,
					},
				}...)
		}
	}
	return envList
}

func getInitContainers(statefulSet *apps.StatefulSet, postgres *api.Postgres) []core.Container {
	statefulSet.Spec.Template.Spec.InitContainers = core_util.UpsertContainer(
		statefulSet.Spec.Template.Spec.InitContainers,
		core.Container{
			Name:           PostgresInitContainerName,
			Image:          PostgresInitContainerImage,
			Resources:      postgres.Spec.PodTemplate.Spec.Resources,
			LivenessProbe:  postgres.Spec.PodTemplate.Spec.LivenessProbe,
			ReadinessProbe: postgres.Spec.PodTemplate.Spec.ReadinessProbe,
			Lifecycle:      postgres.Spec.PodTemplate.Spec.Lifecycle,
		})
	return statefulSet.Spec.Template.Spec.InitContainers
}
func getContainers(statefulSet *apps.StatefulSet, postgres *api.Postgres, postgresVersion *catalog.PostgresVersion) []core.Container {
//TODO: need to modify to handle the case if user give lifecycle command
	lifeCycle :=& core.Lifecycle{
		PreStop: &core.Handler{
			Exec: &core.ExecAction{
				Command: []string{"pg_ctl","-m","fast","-w","stop"},
			},
		},
	}


	statefulSet.Spec.Template.Spec.Containers = core_util.UpsertContainer(
		statefulSet.Spec.Template.Spec.Containers,
		core.Container{
			Name:           api.ResourceSingularPostgres,
			Image:          postgresVersion.Spec.DB.Image,
			Resources:      postgres.Spec.PodTemplate.Spec.Resources,
			LivenessProbe:  postgres.Spec.PodTemplate.Spec.LivenessProbe,
			ReadinessProbe: postgres.Spec.PodTemplate.Spec.ReadinessProbe,
			//TODO: this commented one was the default one.
			//Lifecycle:      postgres.Spec.PodTemplate.Spec.Lifecycle,
			Lifecycle : lifeCycle,
			SecurityContext: &core.SecurityContext{
				Privileged: types.BoolP(false),
				Capabilities: &core.Capabilities{
					Add: []core.Capability{"IPC_LOCK", "SYS_RESOURCE"},
				},
			},
		})
	statefulSet.Spec.Template.Spec.Containers = core_util.UpsertContainer(
		statefulSet.Spec.Template.Spec.Containers,
		core.Container{
			Name:           api.PostgresLeaderElectionContainerName,
			Image:          LeaderElectionImage,
			Resources:      postgres.Spec.PodTemplate.Spec.Resources,
			LivenessProbe:  postgres.Spec.PodTemplate.Spec.LivenessProbe,
			ReadinessProbe: postgres.Spec.PodTemplate.Spec.ReadinessProbe,
			Lifecycle:      postgres.Spec.PodTemplate.Spec.Lifecycle,
			SecurityContext: &core.SecurityContext{
				Privileged: types.BoolP(false),
				Capabilities: &core.Capabilities{
					Add: []core.Capability{"IPC_LOCK", "SYS_RESOURCE"},
				},
			},
		})
	return statefulSet.Spec.Template.Spec.Containers
}

// adding tls key , cert and ca-cert
func upsertTLSVolume(sts *apps.StatefulSet, db *api.Postgres) *apps.StatefulSet {
	for i, container := range sts.Spec.Template.Spec.Containers {
		if container.Name == "exporter" {
			volumeMount := core.VolumeMount{
				Name:      exporterTlsVolumename,
				MountPath: clientTlsVolumeMountPath,
			}
			volumeMounts := container.VolumeMounts
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, volumeMount)
			sts.Spec.Template.Spec.Containers[i].VolumeMounts = volumeMounts

		} else if container.Name == api.PostgresLeaderElectionContainerName {
			volumeMount := core.VolumeMount{
				Name:      leaderTlsVolumeName,
				MountPath: clientTlsVolumeMountPath,
			}
			volumeMounts := container.VolumeMounts
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, volumeMount)
			sts.Spec.Template.Spec.Containers[i].VolumeMounts = volumeMounts

		}
	}
	for i, initContainer := range sts.Spec.Template.Spec.InitContainers {
		if initContainer.Name == PostgresInitContainerName {
			volumeMount := core.VolumeMount{
				Name:      serverTlsVolumeName,
				MountPath: serverTlsVolumeMountPath,
				ReadOnly:  false,
			}
			volumeMounts := initContainer.VolumeMounts
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, volumeMount)

			clientVolumeMount := core.VolumeMount{
				Name:      clientTlsVolumeName,
				MountPath: clientTlsVolumeMountPath,
				ReadOnly:  false,
			}
			volumeMounts = core_util.UpsertVolumeMount(volumeMounts, clientVolumeMount)
			sts.Spec.Template.Spec.InitContainers[i].VolumeMounts = volumeMounts

		}
	}
	serverVolume := core.Volume{
		Name: serverTlsVolumeName,
		VolumeSource: core.VolumeSource{
			Secret: &core.SecretVolumeSource{
				SecretName: db.MustCertSecretName(api.PostgresServerCert),
				Items: []core.KeyToPath{
					{
						Key:  "ca.crt",
						Path: "ca.crt",
					},
					{
						Key:  "tls.crt",
						Path: "server.crt",
					},
					{
						Key:  "tls.key",
						Path: "server.key",
					},
				},
			},
		},
	}
	clientVolume := core.Volume{
		Name: clientTlsVolumeName,
		VolumeSource: core.VolumeSource{
			Secret: &core.SecretVolumeSource{
				SecretName: db.MustCertSecretName(api.PostgresClientCert),
				Items: []core.KeyToPath{
					{
						Key:  "tls.crt",
						Path: "client.crt",
					},
					{
						Key:  "tls.key",
						Path: "client.key",
					},
				},
			},
		},
	}

	exporterTLSVolume := core.Volume{
		Name: exporterTlsVolumename,
		VolumeSource: core.VolumeSource{
			Secret: &core.SecretVolumeSource{
				DefaultMode: pointer.Int32P(0600),
				SecretName:  db.MustCertSecretName(api.PostgresMetricsExporterCert),
				Items: []core.KeyToPath{
					{
						Key:  "ca.crt",
						Path: "ca.crt",
					},
					{
						Key:  "tls.crt",
						Path: "exporter.crt",
					},
					{
						Key:  "tls.key",
						Path: "exporter.key",
					},
				},
			},
		},
	}
	leaderElectionTLSVolume := core.Volume{
		Name: leaderTlsVolumeName,
		VolumeSource: core.VolumeSource{
			Secret: &core.SecretVolumeSource{
				DefaultMode: pointer.Int32P(0600),
				SecretName:  db.MustCertSecretName(api.PostgresClientCert),
				Items: []core.KeyToPath{
					{
						Key:  "ca.crt",
						Path: "ca.crt",
					},
					{
						Key:  "tls.crt",
						Path: "client.crt",
					},
					{
						Key:  "tls.key",
						Path: "client.key",
					},
				},
			},
		},
	}

	sts.Spec.Template.Spec.Volumes = core_util.UpsertVolume(
		sts.Spec.Template.Spec.Volumes,
		serverVolume,
		clientVolume,
		exporterTLSVolume,
		leaderElectionTLSVolume,
	)

	return sts
}
