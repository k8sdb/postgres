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

package framework

//
//func (f *Framework) EventuallyWalDataFound(postgres *api.Postgres) GomegaAsyncAssertion {
//	var err error
//	defer func() {
//		if err != nil {
//			fmt.Println(err)
//		}
//	}()
//	return Eventually(
//		func() bool {
//			var found bool
//			if f.IsMinio(postgres.Spec.Archiver.Storage) { // if it is minio
//				found, err = f.checkMinioWalData(postgres)
//			} else {
//				found, err = f.checkWalData(postgres)
//			}
//			return found
//		},
//		time.Minute*10,
//		time.Second*5,
//	)
//}

//func (f *Framework) checkWalData(postgres *api.Postgres) (bool, error) {
//	cfg, err := storage.NewOSMContext(f.kubeClient, *postgres.Spec.Archiver.Storage, postgres.Namespace)
//	if err != nil {
//		return false, err
//	}
//
//	loc, err := stow.Dial(cfg.Provider, cfg.Config)
//	if err != nil {
//		return false, err
//	}
//	containerID, err := postgres.Spec.Archiver.Storage.Container()
//	if err != nil {
//		return false, err
//	}
//	container, err := loc.Container(containerID)
//	if err != nil {
//		return false, err
//	}
//
//	prefix := controller.WalDataDir(postgres)
//	cursor := stow.CursorStart
//	totalItem := 0
//	for {
//		items, next, err := container.Items(prefix, cursor, 50)
//		if err != nil {
//			return false, err
//		}
//
//		totalItem = totalItem + len(items)
//
//		cursor = next
//		if stow.IsCursorEnd(cursor) {
//			break
//		}
//	}
//
//	return totalItem != 0, nil
//}
//
//func (f *Framework) checkMinioWalData(postgres *api.Postgres) (bool, error) {
//	tunnel, err := f.GetMinioPortForwardingEndPoint()
//	//if tunnel.Local != 0{
//	//	endPoint := fmt.Sprintf("https://%s:%d", localIP, tunnel.Local)
//	//}
//	endPoint := ""
//	if f.IsTLS() {
//		endPoint = fmt.Sprintf("https://%s:%d", localIP, tunnel.Local)
//	} else {
//		endPoint = fmt.Sprintf("http://%s:%d", localIP, tunnel.Local)
//	}
//
//	if err != nil {
//		return false, err
//	}
//	if postgres.Spec.Archiver.Storage != nil {
//		if postgres.Spec.Archiver.Storage.S3 != nil {
//			postgres.Spec.Archiver.Storage.S3.Endpoint = endPoint
//		}
//	}
//	walBool, err := f.checkWalData(postgres)
//	defer tunnel.Close()
//	if err != nil {
//		return false, err
//	}
//	return walBool, nil
//}
