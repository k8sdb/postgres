package api

import "k8s.io/kubernetes/pkg/api"

// StorageSpec defines storage provisioning
type StorageSpec struct {
	// Name of the StorageClass to use when requesting storage provisioning.
	Class string `json:"class"`
	// Persistent Volume Claim
	api.PersistentVolumeClaimSpec `json:",inline,omitempty"`
}

type InitialScriptSpec struct {
	ScriptPath       string `json:"scriptPath,omitempty"`
	api.VolumeSource `json:",inline,omitempty"`
}

type BackupScheduleSpec struct {
	CronExpression string `json:"cronExpression,omitempty"`
	SnapshotSpec   `json:",inline,omitempty"`
}

type SnapshotSpec struct {
	// Snapshot storage secret
	StorageSecret *api.SecretVolumeSource `json:"storageSecret,omitempty"`
	// Database authentication secret
	// +optional
	DatabaseSecret *api.SecretVolumeSource `json:"databaseSecret,omitempty"`
	// Cloud bucket name
	BucketName string `json:"bucketName,omitempty"`
}

type DatabaseStatus string

const (
	// used for Databases that are currently running
	StatusDatabaseRunning DatabaseStatus = "Running"
	// used for Databases that are currently creating
	StatusDatabaseCreating DatabaseStatus = "Creating"
	// used for Databases that are Failed
	StatusDatabaseFailed DatabaseStatus = "Failed"
)
