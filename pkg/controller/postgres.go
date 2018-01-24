package controller

import (
	"fmt"
	"reflect"

	"github.com/appscode/go/log"
	mon_api "github.com/appscode/kube-mon/api"
	"github.com/appscode/kutil"
	core_util "github.com/appscode/kutil/core/v1"
	meta_util "github.com/appscode/kutil/meta"
	api "github.com/kubedb/apimachinery/apis/kubedb/v1alpha1"
	kutildb "github.com/kubedb/apimachinery/client/typed/kubedb/v1alpha1/util"
	"github.com/kubedb/apimachinery/pkg/docker"
	"github.com/kubedb/apimachinery/pkg/eventer"
	"github.com/kubedb/apimachinery/pkg/storage"
	"github.com/kubedb/postgres/pkg/validator"
	core "k8s.io/api/core/v1"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

func (c *Controller) create(postgres *api.Postgres) error {
	if err := validator.ValidatePostgres(c.client, postgres, &c.opt.Docker); err != nil {
		c.recorder.Event(
			postgres.ObjectReference(),
			core.EventTypeWarning,
			eventer.EventReasonInvalid,
			err.Error(),
		)
		return nil // user error so just record error and don't retry.
	}

	if postgres.Status.CreationTime == nil {
		es, _, err := kutildb.PatchPostgres(c.extClient, postgres, func(in *api.Postgres) *api.Postgres {
			t := metav1.Now()
			in.Status.CreationTime = &t
			in.Status.Phase = api.DatabasePhaseCreating
			return in
		})
		if err != nil {
			c.recorder.Eventf(
				postgres.ObjectReference(),
				core.EventTypeWarning,
				eventer.EventReasonFailedToUpdate,
				err.Error(),
			)
			return err
		}
		postgres.Status = es.Status
	}

	// Dynamic Defaulting
	// Assign Default Monitoring Port
	if err := c.setMonitoringPort(postgres); err != nil {
		return err
	}

	// Check DormantDatabase
	// It can be used as resumed
	if err := c.matchDormantDatabase(postgres); err != nil {
		return err
	}

	// create Governing Service
	governingService := c.opt.GoverningService
	if err := c.CreateGoverningService(governingService, postgres.Namespace); err != nil {
		c.recorder.Eventf(
			postgres.ObjectReference(),
			core.EventTypeWarning,
			eventer.EventReasonFailedToCreate,
			`Failed to create ServiceAccount: "%v". Reason: %v`,
			governingService,
			err,
		)
		return err
	}

	// ensure database Service
	vt1, err := c.ensureService(postgres)
	if err != nil {
		return err
	}

	// ensure database StatefulSet
	vt2, err := c.ensurePostgresNode(postgres)
	if err != nil {
		return err
	}

	if vt1 == kutil.VerbCreated && vt2 == kutil.VerbCreated {
		c.recorder.Event(
			postgres.ObjectReference(),
			core.EventTypeNormal,
			eventer.EventReasonSuccessful,
			"Successfully created Postgres",
		)
	} else if vt1 == kutil.VerbPatched || vt2 == kutil.VerbPatched {
		c.recorder.Event(
			postgres.ObjectReference(),
			core.EventTypeNormal,
			eventer.EventReasonSuccessful,
			"Successfully patched Postgres",
		)
	}

	if _, err := meta_util.GetString(postgres.Annotations, api.AnnotationInitialized); err == kutil.ErrNotFound &&
		postgres.Spec.Init != nil &&
		postgres.Spec.Init.SnapshotSource != nil {
		succeeded, err := c.initialize(postgres)
		if err != nil {
			return fmt.Errorf("failed to complete initialization. Reason: %v", err)
		}
		if !succeeded {
			return nil
		}
	}

	if err := c.setInitializedAnnotation(postgres); err != nil {
		return err
	}

	// Ensure Schedule backup
	c.ensureBackupScheduler(postgres)

	if err := c.manageMonitor(postgres); err != nil {
		c.recorder.Eventf(
			postgres.ObjectReference(),
			core.EventTypeWarning,
			eventer.EventReasonFailedToCreate,
			"Failed to manage monitoring system. Reason: %v",
			err,
		)
		log.Errorln(err)
		return nil
	}
	return nil
}

func (c *Controller) setInitializedAnnotation(postgres *api.Postgres) error {
	if _, err := meta_util.GetString(postgres.Annotations, api.AnnotationInitialized); err == kutil.ErrNotFound &&
		postgres.Spec.Init != nil {
		pg, _, err := kutildb.PatchPostgres(c.extClient, postgres, func(in *api.Postgres) *api.Postgres {
			in.Annotations = core_util.UpsertMap(in.Annotations, map[string]string{
				api.AnnotationInitialized: "",
			})
			return in
		})
		if err != nil {
			return err
		}
		postgres.Annotations = pg.Annotations
	}
	return nil
}

// Assign Default Monitoring Port if MonitoringSpec Exists
// and the AgentVendor is Prometheus.
func (c *Controller) setMonitoringPort(postgres *api.Postgres) error {
	if postgres.Spec.Monitor != nil &&
		postgres.GetMonitoringVendor() == mon_api.VendorPrometheus {
		if postgres.Spec.Monitor.Prometheus == nil {
			postgres.Spec.Monitor.Prometheus = &mon_api.PrometheusSpec{}
		}
		if postgres.Spec.Monitor.Prometheus.Port == 0 {
			pg, _, err := kutildb.PatchPostgres(c.extClient, postgres, func(in *api.Postgres) *api.Postgres {
				in.Spec.Monitor.Prometheus.Port = api.PrometheusExporterPortNumber
				return in
			})

			if err != nil {
				c.recorder.Eventf(
					postgres.ObjectReference(),
					core.EventTypeWarning,
					eventer.EventReasonFailedToUpdate,
					err.Error(),
				)
				return err
			}
			postgres.Spec.Monitor = pg.Spec.Monitor
		}
	}
	return nil
}

func (c *Controller) matchDormantDatabase(postgres *api.Postgres) error {
	// Check if DormantDatabase exists or not
	dormantDb, err := c.extClient.DormantDatabases(postgres.Namespace).Get(postgres.Name, metav1.GetOptions{})
	if err != nil {
		if !kerr.IsNotFound(err) {
			c.recorder.Eventf(
				postgres.ObjectReference(),
				core.EventTypeWarning,
				eventer.EventReasonFailedToGet,
				`Fail to get DormantDatabase: "%v". Reason: %v`,
				postgres.Name,
				err,
			)
			return err
		}
		return nil
	}

	var sendEvent = func(message string, args ...interface{}) error {
		c.recorder.Eventf(
			postgres.ObjectReference(),
			core.EventTypeWarning,
			eventer.EventReasonFailedToCreate,
			message,
			args,
		)
		return fmt.Errorf(message, args)
	}

	// Check DatabaseKind
	if dormantDb.Labels[api.LabelDatabaseKind] != api.ResourceKindPostgres {
		return sendEvent(fmt.Sprintf(`Invalid Postgres: "%v". Exists DormantDatabase "%v" of different Kind`,
			postgres.Name, dormantDb.Name))
	}

	// Check Origin Spec
	drmnOriginSpec := dormantDb.Spec.Origin.Spec.Postgres
	originalSpec := postgres.Spec

	if originalSpec.DatabaseSecret == nil {
		originalSpec.DatabaseSecret = &core.SecretVolumeSource{
			SecretName: postgres.Name + "-auth",
		}
	}
	if !reflect.DeepEqual(drmnOriginSpec, &originalSpec) {
		return sendEvent("Postgres spec mismatches with OriginSpec in DormantDatabases")
	}

	if err := c.setInitializedAnnotation(postgres); err != nil {
		c.recorder.Eventf(postgres.ObjectReference(), core.EventTypeWarning, eventer.EventReasonFailedToUpdate, err.Error())
		return err
	}

	return kutildb.DeleteDormantDatabase(c.extClient, dormantDb.ObjectMeta)
}

func (c *Controller) ensurePostgresNode(postgres *api.Postgres) (kutil.VerbType, error) {
	var err error

	if err = c.ensureDatabaseSecret(postgres); err != nil {
		return kutil.VerbUnchanged, err
	}

	if c.opt.EnableRbac {
		// Ensure ClusterRoles for database statefulsets
		if err := c.ensureRBACStuff(postgres); err != nil {
			return kutil.VerbUnchanged, err
		}
	}

	vt, err := c.ensureCombinedNode(postgres)
	if err != nil {
		return kutil.VerbUnchanged, err
	}

	pg, _, err := kutildb.PatchPostgres(c.extClient, postgres, func(in *api.Postgres) *api.Postgres {
		in.Status.Phase = api.DatabasePhaseRunning
		return in
	})
	if err != nil {
		c.recorder.Eventf(postgres.ObjectReference(), core.EventTypeWarning, eventer.EventReasonFailedToUpdate, err.Error())
		return kutil.VerbUnchanged, err
	}
	postgres.Status = pg.Status

	return vt, nil
}

func (c *Controller) ensureBackupScheduler(postgres *api.Postgres) {
	kutildb.AssignTypeKind(postgres)
	// Setup Schedule backup
	if postgres.Spec.BackupSchedule != nil {
		err := c.cronController.ScheduleBackup(postgres, postgres.ObjectMeta, postgres.Spec.BackupSchedule)
		if err != nil {
			c.recorder.Eventf(
				postgres.ObjectReference(),
				core.EventTypeWarning,
				eventer.EventReasonFailedToSchedule,
				"Failed to schedule snapshot. Reason: %v",
				err,
			)
			log.Errorln(err)
		}
	} else {
		c.cronController.StopBackupScheduling(postgres.ObjectMeta)
	}
}

func (c *Controller) initialize(postgres *api.Postgres) (bool, error) {
	pg, _, err := kutildb.PatchPostgres(c.extClient, postgres, func(in *api.Postgres) *api.Postgres {
		in.Status.Phase = api.DatabasePhaseInitializing
		return in
	})
	if err != nil {
		c.recorder.Eventf(postgres, core.EventTypeWarning, eventer.EventReasonFailedToUpdate, err.Error())
		return false, err
	}
	postgres.Status = pg.Status

	if err := docker.CheckDockerImageVersion(c.opt.Docker.GetToolsImage(postgres), string(postgres.Spec.Version)); err != nil {
		return false, fmt.Errorf(`image %s not found`, c.opt.Docker.GetToolsImageWithTag(postgres))
	}

	snapshotSource := postgres.Spec.Init.SnapshotSource
	// Event for notification that kubernetes objects are creating
	c.recorder.Eventf(
		postgres.ObjectReference(),
		core.EventTypeNormal,
		eventer.EventReasonInitializing,
		`Initializing from Snapshot: "%v"`,
		snapshotSource.Name,
	)

	namespace := snapshotSource.Namespace
	if namespace == "" {
		namespace = postgres.Namespace
	}
	snapshot, err := c.extClient.Snapshots(namespace).Get(snapshotSource.Name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}

	secret, err := storage.NewOSMSecret(c.client, snapshot)
	if err != nil {
		return false, err
	}
	secret, err = c.client.CoreV1().Secrets(secret.Namespace).Create(secret)
	if err != nil {
		return false, err
	}

	job, err := c.createRestoreJob(postgres, snapshot)
	if err != nil {
		return false, err
	}

	if err := c.SetJobOwnerReference(snapshot, job); err != nil {
		return false, err
	}

	wait.PollImmediate(kutil.RetryInterval, kutil.ReadinessTimeout, func() (bool, error) {
		pg, err := c.extClient.Postgreses(postgres.Namespace).Get(postgres.Name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if pg.Status.Phase != api.DatabasePhaseInitializing {
			postgres.Status = pg.Status
			return true, nil
		}
		return false, nil
	})

	switch postgres.Status.Phase {
	case api.DatabasePhaseRunning:
		c.recorder.Event(
			postgres.ObjectReference(),
			core.EventTypeNormal,
			eventer.EventReasonSuccessfulInitialize,
			"Successfully completed initialization",
		)
		return true, nil
	case api.DatabasePhaseFailed:
		c.recorder.Event(
			postgres.ObjectReference(),
			core.EventTypeWarning,
			eventer.EventReasonFailedToInitialize,
			"Failed to complete initialization",
		)
		return false, nil
	default:
		kutildb.PatchPostgres(c.extClient, postgres, func(in *api.Postgres) *api.Postgres {
			in.Status.Phase = api.DatabasePhaseFailed
			in.Status.Reason = "Failed to complete initialization"
			return in
		})
		return false, nil
	}
}

func (c *Controller) pause(postgres *api.Postgres) error {

	c.recorder.Event(postgres.ObjectReference(), core.EventTypeNormal, eventer.EventReasonPausing, "Pausing Postgres")

	if _, err := c.createDormantDatabase(postgres); err != nil {
		c.recorder.Eventf(
			postgres.ObjectReference(),
			core.EventTypeWarning,
			eventer.EventReasonFailedToCreate,
			`Failed to create DormantDatabase: "%v". Reason: %v`,
			postgres.Name,
			err,
		)
		return err
	}
	c.recorder.Eventf(
		postgres.ObjectReference(),
		core.EventTypeNormal,
		eventer.EventReasonSuccessfulCreate,
		`Successfully created DormantDatabase: "%v"`,
		postgres.Name,
	)

	c.cronController.StopBackupScheduling(postgres.ObjectMeta)

	if postgres.Spec.Monitor != nil {
		if _, err := c.deleteMonitor(postgres); err != nil {
			c.recorder.Eventf(
				postgres.ObjectReference(),
				core.EventTypeWarning,
				eventer.EventReasonFailedToDelete,
				"Failed to delete monitoring system. Reason: %v",
				err,
			)
			log.Errorln(err)
			return nil
		}
	}
	return nil
}

func (c *Controller) GetDatabasePhase(meta metav1.ObjectMeta) (api.DatabasePhase, error) {
	postgres, err := c.extClient.Postgreses(meta.Namespace).Get(meta.Name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return postgres.Status.Phase, nil
}

func (c *Controller) SetDatabaseStatus(meta metav1.ObjectMeta, phase api.DatabasePhase, reason string) error {
	postgres, err := c.extClient.Postgreses(meta.Namespace).Get(meta.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	_, _, err = kutildb.PatchPostgres(c.extClient, postgres, func(in *api.Postgres) *api.Postgres {
		in.Status.Phase = phase
		in.Status.Reason = reason
		return in
	})
	return err
}
