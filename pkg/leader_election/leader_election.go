package leader_election

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/appscode/go/io"
	kutil "github.com/appscode/kutil/core/v1"
	core "k8s.io/api/core/v1"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
)

const (
	RolePrimary = "primary"
	RoleReplica = "replica"
)

func RunLeaderElection() {

	leaderElectionLease := 3 * time.Second

	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	hostname, err := os.Hostname()
	if err != nil {
		log.Fatalln(err)
	}

	parts := strings.Split(hostname, "-")
	statefulsetName := strings.Join(parts[:len(parts)-1], "-")
	configMapName := fmt.Sprintf("%v-leader-lock", statefulsetName)

	fmt.Println(fmt.Sprintf(`We want "%v" as our leader`, hostname))

	config, err := restclient.InClusterConfig()
	if err != nil {
		log.Fatalln(err)
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalln(err.Error())
	}

	configMap := &core.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
		},
	}
	if _, err := kubeClient.CoreV1().ConfigMaps(namespace).Create(configMap); err != nil && !kerr.IsAlreadyExists(err) {
		log.Fatalln(err)
	}

	resLock := &resourcelock.ConfigMapLock{
		ConfigMapMeta: configMap.ObjectMeta,
		Client:        kubeClient.CoreV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity:      hostname,
			EventRecorder: &record.FakeRecorder{},
		},
	}

	runningFirstTime := true

	go func() {
		leaderelection.RunOrDie(leaderelection.LeaderElectionConfig{
			Lock:          resLock,
			LeaseDuration: leaderElectionLease,
			RenewDeadline: leaderElectionLease * 2 / 3,
			RetryPeriod:   leaderElectionLease / 3,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(stop <-chan struct{}) {
					fmt.Println("Got leadership, now do your jobs")
				},
				OnStoppedLeading: func() {
					fmt.Println("Lost leadership, now quit")
					os.Exit(1)
				},
				OnNewLeader: func(identity string) {

					role := RoleReplica

					if identity == hostname {
						role = RolePrimary
						statefulSet, err := kubeClient.AppsV1beta1().StatefulSets(namespace).Get(statefulsetName, metav1.GetOptions{})
						if err != nil {
							log.Fatalln(err)
						}

						pods, err := kubeClient.CoreV1().Pods(namespace).List(metav1.ListOptions{
							LabelSelector: metav1.FormatLabelSelector(statefulSet.Spec.Selector),
						})
						if err != nil {
							log.Fatalln(err)
						}

						var primaryPod metav1.ObjectMeta
						for _, pod := range pods.Items {
							_, err = kutil.TryPatchPod(kubeClient, pod.ObjectMeta, func(in *core.Pod) *core.Pod {
								in.Labels["kubedb.com/role"] = RoleReplica
								return in
							})
							if err != nil && !kerr.IsNotFound(err) {
								log.Fatalln(err)
							}

							if pod.Name == hostname {
								primaryPod = pod.ObjectMeta
							}
						}

						_, err = kutil.TryPatchPod(kubeClient, primaryPod, func(in *core.Pod) *core.Pod {
							in.Labels["kubedb.com/role"] = RolePrimary
							return in
						})
						if err != nil {
							log.Fatalln(err)
						}
					}

					if runningFirstTime {
						runningFirstTime = false
						go func() {
							// su-exec postgres /scripts/primary/run.sh
							cmd := exec.Command("su-exec", "postgres", fmt.Sprintf("/scripts/%s/run.sh", role))
							cmd.Stdout = os.Stdout
							cmd.Stderr = os.Stderr

							if err = cmd.Run(); err != nil {
								log.Println(err)
							}
							os.Exit(1)
						}()
					} else {
						if identity == hostname {
							if !io.WriteString("/tmp/pg-failover-trigger", "") {
								log.Fatalln("Failed to create trigger file")
							}
						}
					}
				},
			},
		})
	}()

	select {}
}