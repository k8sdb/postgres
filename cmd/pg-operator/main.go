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

package main

import (
	"kubedb.dev/postgres/pkg/cmds"

	_ "go.bytebuilders.dev/license-verifier/info"
	"gomodules.xyz/kglog"
	_ "k8s.io/client-go/kubernetes/fake"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/klog/v2"
)

func main() {
	rootCmd := cmds.NewRootCmd(Version)
	kglog.Init(rootCmd, true)
	defer kglog.FlushLogs()

	if err := rootCmd.Execute(); err != nil {
		klog.Fatal(err)
	}
}
