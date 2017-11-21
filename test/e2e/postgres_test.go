package e2e_test

import (
	"fmt"
	"os"

	"github.com/appscode/go/types"
	tapi "github.com/k8sdb/apimachinery/apis/kubedb/v1alpha1"
	"github.com/k8sdb/postgres/test/e2e/framework"
	"github.com/k8sdb/postgres/test/e2e/matcher"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	S3_BUCKET_NAME       = "S3_BUCKET_NAME"
	GCS_BUCKET_NAME      = "GCS_BUCKET_NAME"
	AZURE_CONTAINER_NAME = "AZURE_CONTAINER_NAME"
	SWIFT_CONTAINER_NAME = "SWIFT_CONTAINER_NAME"
)

var _ = Describe("Postgres", func() {
	var (
		err         error
		f           *framework.Invocation
		postgres    *tapi.Postgres
		snapshot    *tapi.Snapshot
		secret      *core.Secret
		skipMessage string
	)

	BeforeEach(func() {
		f = root.Invoke()
		postgres = f.Postgres()
		snapshot = f.Snapshot()
		skipMessage = ""
	})

	var createAndWaitForRunning = func() {
		By("Create Postgres: " + postgres.Name)
		err = f.CreatePostgres(postgres)
		Expect(err).NotTo(HaveOccurred())

		By("Wait for Running postgres")
		f.EventuallyPostgresRunning(postgres.ObjectMeta).Should(BeTrue())
	}

	var deleteTestResource = func() {
		By("Delete postgres")
		err = f.DeletePostgres(postgres.ObjectMeta)
		Expect(err).NotTo(HaveOccurred())

		By("Wait for postgres to be paused")
		f.EventuallyDormantDatabaseStatus(postgres.ObjectMeta).Should(matcher.HavePaused())

		By("WipeOut postgres")
		_, err := f.TryPatchDormantDatabase(postgres.ObjectMeta, func(in *tapi.DormantDatabase) *tapi.DormantDatabase {
			in.Spec.WipeOut = true
			return in
		})
		Expect(err).NotTo(HaveOccurred())

		By("Wait for postgres to be wipedOut")
		f.EventuallyDormantDatabaseStatus(postgres.ObjectMeta).Should(matcher.HaveWipedOut())

		err = f.DeleteDormantDatabase(postgres.ObjectMeta)
		Expect(err).NotTo(HaveOccurred())
	}

	var shouldSuccessfullyRunning = func() {
		if skipMessage != "" {
			Skip(skipMessage)
		}

		// Create Postgres
		createAndWaitForRunning()

		// Delete test resource
		deleteTestResource()
	}

	Describe("Test", func() {

		Context("General", func() {

			Context("-", func() {
				It("should run successfully", shouldSuccessfullyRunning)
			})

			Context("With PVC", func() {
				BeforeEach(func() {
					if f.StorageClass == "" {
						skipMessage = "Missing StorageClassName. Provide as flag to test this."
					}
					postgres.Spec.Storage = &core.PersistentVolumeClaimSpec{
						Resources: core.ResourceRequirements{
							Requests: core.ResourceList{
								core.ResourceStorage: resource.MustParse("5Gi"),
							},
						},
						StorageClassName: types.StringP(f.StorageClass),
					}
				})
				It("should run successfully", shouldSuccessfullyRunning)
			})
		})

		Context("DoNotPause", func() {
			BeforeEach(func() {
				postgres.Spec.DoNotPause = true
			})

			It("should work successfully", func() {
				// Create and wait for running Postgres
				createAndWaitForRunning()

				By("Delete postgres")
				err = f.DeletePostgres(postgres.ObjectMeta)
				Expect(err).NotTo(HaveOccurred())

				By("Postgres is not paused. Check for postgres")
				f.EventuallyPostgres(postgres.ObjectMeta).Should(BeTrue())

				By("Check for Running postgres")
				f.EventuallyPostgresRunning(postgres.ObjectMeta).Should(BeTrue())

				By("Update postgres to set DoNotPause=false")
				f.TryPatchPostgres(postgres.ObjectMeta, func(in *tapi.Postgres) *tapi.Postgres {
					in.Spec.DoNotPause = false
					return in
				})

				// Delete test resource
				deleteTestResource()
			})
		})

		Context("Snapshot", func() {
			var skipDataCheck bool

			AfterEach(func() {
				f.DeleteSecret(secret.ObjectMeta)
			})

			BeforeEach(func() {
				skipDataCheck = false
				snapshot.Spec.DatabaseName = postgres.Name
			})

			var shouldTakeSnapshot = func() {
				// Create and wait for running Postgres
				createAndWaitForRunning()

				By("Create Secret")
				f.CreateSecret(secret)

				By("Create Snapshot")
				f.CreateSnapshot(snapshot)

				By("Check for Successed snapshot")
				f.EventuallySnapshotPhase(snapshot.ObjectMeta).Should(Equal(tapi.SnapshotPhaseSuccessed))

				if !skipDataCheck {
					By("Check for snapshot data")
					f.EventuallySnapshotDataFound(snapshot).Should(BeTrue())
				}

				// Delete test resource
				deleteTestResource()

				if !skipDataCheck {
					By("Check for snapshot data")
					f.EventuallySnapshotDataFound(snapshot).Should(BeFalse())
				}
			}

			Context("In Local", func() {
				BeforeEach(func() {
					skipDataCheck = true
					secret = f.SecretForLocalBackend()
					snapshot.Spec.StorageSecretName = secret.Name
					snapshot.Spec.Local = &tapi.LocalSpec{
						Path: "/repo",
						VolumeSource: core.VolumeSource{
							HostPath: &core.HostPathVolumeSource{
								Path: "/repo",
							},
						},
					}
				})

				It("should take Snapshot successfully", shouldTakeSnapshot)
			})

			Context("In S3", func() {
				BeforeEach(func() {
					secret = f.SecretForS3Backend()
					snapshot.Spec.StorageSecretName = secret.Name
					snapshot.Spec.S3 = &tapi.S3Spec{
						Bucket: os.Getenv(S3_BUCKET_NAME),
					}
				})

				It("should take Snapshot successfully", shouldTakeSnapshot)
			})

			Context("In GCS", func() {
				BeforeEach(func() {
					secret = f.SecretForGCSBackend()
					snapshot.Spec.StorageSecretName = secret.Name
					snapshot.Spec.GCS = &tapi.GCSSpec{
						Bucket: os.Getenv(GCS_BUCKET_NAME),
					}
				})

				It("should take Snapshot successfully", shouldTakeSnapshot)
			})

			Context("In Azure", func() {
				BeforeEach(func() {
					secret = f.SecretForAzureBackend()
					snapshot.Spec.StorageSecretName = secret.Name
					snapshot.Spec.Azure = &tapi.AzureSpec{
						Container: os.Getenv(AZURE_CONTAINER_NAME),
					}
				})

				It("should take Snapshot successfully", shouldTakeSnapshot)
			})

			Context("In Swift", func() {
				BeforeEach(func() {
					secret = f.SecretForSwiftBackend()
					snapshot.Spec.StorageSecretName = secret.Name
					snapshot.Spec.Swift = &tapi.SwiftSpec{
						Container: os.Getenv(SWIFT_CONTAINER_NAME),
					}
				})

				It("should take Snapshot successfully", shouldTakeSnapshot)
			})
		})

		Context("Initialize", func() {
			Context("With Script", func() {
				BeforeEach(func() {
					postgres.Spec.Init = &tapi.InitSpec{
						ScriptSource: &tapi.ScriptSourceSpec{
							ScriptPath: "postgres-init-scripts/run.sh",
							VolumeSource: core.VolumeSource{
								GitRepo: &core.GitRepoVolumeSource{
									Repository: "https://github.com/k8sdb/postgres-init-scripts.git",
								},
							},
						},
					}
				})

				It("should run successfully", shouldSuccessfullyRunning)

			})

			Context("With Snapshot", func() {
				AfterEach(func() {
					f.DeleteSecret(secret.ObjectMeta)
				})

				var shouldRestoreSnapshot = func() {
					// Create and wait for running Postgres
					createAndWaitForRunning()

					By("Create Secret")
					f.CreateSecret(secret)

					By("Create Snapshot")
					f.CreateSnapshot(snapshot)

					By("Check for Successed snapshot")
					f.EventuallySnapshotPhase(snapshot.ObjectMeta).Should(Equal(tapi.SnapshotPhaseSuccessed))

					By("Check for snapshot data")
					f.EventuallySnapshotDataFound(snapshot).Should(BeTrue())

					oldPostgres, err := f.GetPostgres(postgres.ObjectMeta)
					Expect(err).NotTo(HaveOccurred())

					By("Create postgres from snapshot")
					postgres = f.Postgres()
					postgres.Spec.Init = &tapi.InitSpec{
						SnapshotSource: &tapi.SnapshotSourceSpec{
							Namespace: snapshot.Namespace,
							Name:      snapshot.Name,
						},
					}

					// Create and wait for running Postgres
					createAndWaitForRunning()

					// Delete test resource
					deleteTestResource()
					postgres = oldPostgres
					// Delete test resource
					deleteTestResource()
				}

				Context("with S3", func() {
					BeforeEach(func() {
						secret = f.SecretForS3Backend()
						snapshot.Spec.StorageSecretName = secret.Name
						snapshot.Spec.S3 = &tapi.S3Spec{
							Bucket: os.Getenv(S3_BUCKET_NAME),
						}
						snapshot.Spec.DatabaseName = postgres.Name
					})

					It("should run successfully", shouldRestoreSnapshot)
				})

				Context("with GCS", func() {
					BeforeEach(func() {
						secret = f.SecretForGCSBackend()
						snapshot.Spec.StorageSecretName = secret.Name
						snapshot.Spec.GCS = &tapi.GCSSpec{
							Bucket: os.Getenv(GCS_BUCKET_NAME),
						}
						snapshot.Spec.DatabaseName = postgres.Name
					})

					It("should run successfully", shouldRestoreSnapshot)
				})
			})
		})

		Context("Resume", func() {
			var usedInitSpec bool
			BeforeEach(func() {
				usedInitSpec = false
			})

			var shouldResumeSuccessfully = func() {
				// Create and wait for running Postgres
				createAndWaitForRunning()

				By("Delete postgres")
				f.DeletePostgres(postgres.ObjectMeta)

				By("Wait for postgres to be paused")
				f.EventuallyDormantDatabaseStatus(postgres.ObjectMeta).Should(matcher.HavePaused())

				_, err = f.TryPatchDormantDatabase(postgres.ObjectMeta, func(in *tapi.DormantDatabase) *tapi.DormantDatabase {
					in.Spec.Resume = true
					return in
				})
				Expect(err).NotTo(HaveOccurred())

				By("Wait for DormantDatabase to be deleted")
				f.EventuallyDormantDatabase(postgres.ObjectMeta).Should(BeFalse())

				By("Wait for Running postgres")
				f.EventuallyPostgresRunning(postgres.ObjectMeta).Should(BeTrue())

				postgres, err = f.GetPostgres(postgres.ObjectMeta)
				Expect(err).NotTo(HaveOccurred())

				if usedInitSpec {
					Expect(postgres.Spec.Init).Should(BeNil())
					Expect(postgres.Annotations[tapi.PostgresInitSpec]).ShouldNot(BeEmpty())
				}

				// Delete test resource
				deleteTestResource()
			}

			Context("-", func() {
				It("should resume DormantDatabase successfully", shouldResumeSuccessfully)
			})

			Context("With Init", func() {
				BeforeEach(func() {
					usedInitSpec = true
					postgres.Spec.Init = &tapi.InitSpec{
						ScriptSource: &tapi.ScriptSourceSpec{
							ScriptPath: "postgres-init-scripts/run.sh",
							VolumeSource: core.VolumeSource{
								GitRepo: &core.GitRepoVolumeSource{
									Repository: "https://github.com/k8sdb/postgres-init-scripts.git",
								},
							},
						},
					}
				})

				It("should resume DormantDatabase successfully", shouldResumeSuccessfully)
			})

			Context("With original Postgres", func() {
				It("should resume DormantDatabase successfully", func() {
					// Create and wait for running Postgres
					createAndWaitForRunning()

					By("Delete postgres")
					f.DeletePostgres(postgres.ObjectMeta)

					By("Wait for postgres to be paused")
					f.EventuallyDormantDatabaseStatus(postgres.ObjectMeta).Should(matcher.HavePaused())

					// Create Postgres object again to resume it
					By("Create Postgres: " + postgres.Name)
					err = f.CreatePostgres(postgres)

					if err != nil {
						fmt.Println(err)
					}
					Expect(err).NotTo(HaveOccurred())

					By("Wait for DormantDatabase to be deleted")
					f.EventuallyDormantDatabase(postgres.ObjectMeta).Should(BeFalse())

					By("Wait for Running postgres")
					f.EventuallyPostgresRunning(postgres.ObjectMeta).Should(BeTrue())

					postgres, err = f.GetPostgres(postgres.ObjectMeta)
					Expect(err).NotTo(HaveOccurred())

					// Delete test resource
					deleteTestResource()
				})
				Context("with init", func() {
					BeforeEach(func() {
						usedInitSpec = true
						postgres.Spec.Init = &tapi.InitSpec{
							ScriptSource: &tapi.ScriptSourceSpec{
								ScriptPath: "postgres-init-scripts/run.sh",
								VolumeSource: core.VolumeSource{
									GitRepo: &core.GitRepoVolumeSource{
										Repository: "https://github.com/k8sdb/postgres-init-scripts.git",
									},
								},
							},
						}
					})

					It("should resume DormantDatabase successfully", func() {
						// Create and wait for running Postgres
						createAndWaitForRunning()

						for i := 0; i < 3; i++ {
							By(fmt.Sprintf("%v-th", i+1) + " time running.")
							By("Delete postgres")
							f.DeletePostgres(postgres.ObjectMeta)

							By("Wait for postgres to be paused")
							f.EventuallyDormantDatabaseStatus(postgres.ObjectMeta).Should(matcher.HavePaused())

							// Create Postgres object again to resume it
							By("Create Postgres: " + postgres.Name)
							err = f.CreatePostgres(postgres)
							Expect(err).NotTo(HaveOccurred())

							By("Wait for DormantDatabase to be deleted")
							f.EventuallyDormantDatabase(postgres.ObjectMeta).Should(BeFalse())

							By("Wait for Running postgres")
							f.EventuallyPostgresRunning(postgres.ObjectMeta).Should(BeTrue())

							_, err := f.GetPostgres(postgres.ObjectMeta)
							Expect(err).NotTo(HaveOccurred())
						}

						// Delete test resource
						deleteTestResource()
					})
				})
			})
		})

		Context("SnapshotScheduler", func() {
			AfterEach(func() {
				f.DeleteSecret(secret.ObjectMeta)
			})

			BeforeEach(func() {
				secret = f.SecretForLocalBackend()
			})

			Context("With Startup", func() {
				BeforeEach(func() {
					postgres.Spec.BackupSchedule = &tapi.BackupScheduleSpec{
						CronExpression: "@every 1m",
						SnapshotStorageSpec: tapi.SnapshotStorageSpec{
							StorageSecretName: secret.Name,
							Local: &tapi.LocalSpec{
								Path: "/repo",
								VolumeSource: core.VolumeSource{
									HostPath: &core.HostPathVolumeSource{
										Path: "/repo",
									},
								},
							},
						},
					}
				})

				It("should run schedular successfully", func() {
					By("Create Secret")
					f.CreateSecret(secret)

					// Create and wait for running Postgres
					createAndWaitForRunning()

					By("Count multiple Snapshot")
					f.EventuallySnapshotCount(postgres.ObjectMeta).Should(matcher.MoreThan(3))

					deleteTestResource()
				})
			})

			Context("With Update", func() {
				It("should run schedular successfully", func() {
					// Create and wait for running Postgres
					createAndWaitForRunning()

					By("Create Secret")
					f.CreateSecret(secret)

					By("Update postgres")
					_, err = f.TryPatchPostgres(postgres.ObjectMeta, func(in *tapi.Postgres) *tapi.Postgres {
						in.Spec.BackupSchedule = &tapi.BackupScheduleSpec{
							CronExpression: "@every 1m",
							SnapshotStorageSpec: tapi.SnapshotStorageSpec{
								StorageSecretName: secret.Name,
								Local: &tapi.LocalSpec{
									Path: "/repo",
									VolumeSource: core.VolumeSource{
										HostPath: &core.HostPathVolumeSource{
											Path: "/repo",
										},
									},
								},
							},
						}

						return in
					})
					Expect(err).NotTo(HaveOccurred())

					By("Count multiple Snapshot")
					f.EventuallySnapshotCount(postgres.ObjectMeta).Should(matcher.MoreThan(3))

					deleteTestResource()
				})
			})
		})

	})
})
