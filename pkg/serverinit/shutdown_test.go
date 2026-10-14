/*
Copyright 2026 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package serverinit_test

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/serverinit"
)

type shutdownTestStorage struct {
	blobserver.Storage
	close func() error
}

func (s shutdownTestStorage) Close() error { return s.close() }

type shutdownTestHandler struct {
	http.Handler
	close   func() error
	initErr error
}

func (h shutdownTestHandler) Close() error { return h.close() }
func (h shutdownTestHandler) InitHandler(blobserver.FindHandlerByTyper) error {
	return h.initErr
}

var shutdownTestConstructorID atomic.Uint64

func TestHTTPHandlerShutdownBeforeStorage(t *testing.T) {
	for _, failInit := range []bool{false, true} {
		t.Run(fmt.Sprintf("failInit=%v", failInit), func(t *testing.T) {
			id := shutdownTestConstructorID.Add(1)
			storageType := fmt.Sprintf("shutdown-test-storage-%d", id)
			handlerType := fmt.Sprintf("shutdown-test-handler-%d", id)
			var closed []string
			closeErr := errors.New("test handler close error")
			blobserver.RegisterStorageConstructor(storageType, func(blobserver.Loader, jsonconfig.Obj) (blobserver.Storage, error) {
				return shutdownTestStorage{close: func() error {
					closed = append(closed, "storage")
					return nil
				}}, nil
			})
			blobserver.RegisterHandlerConstructor(handlerType, func(ld blobserver.Loader, _ jsonconfig.Obj) (http.Handler, error) {
				// Force storage to be constructed first, as it typically is
				// for a handler with a background importer using that store.
				if _, err := ld.GetStorage("/storage/"); err != nil {
					return nil, err
				}
				h := shutdownTestHandler{close: func() error {
					closed = append(closed, "handler")
					return closeErr
				}}
				if failInit {
					h.initErr = errors.New("test handler initialization error")
				}
				return h, nil
			})
			config := serverinit.ExportNewConfigFromObj(jsonconfig.Obj{
				"auth": "none",
				"prefixes": map[string]any{
					"/storage/": map[string]any{
						"handler": "storage-" + storageType, "internal": true,
						"handlerArgs": map[string]any{},
					},
					"/handler/": map[string]any{
						"handler": handlerType, "handlerArgs": map[string]any{},
					},
				},
			})
			shutdown, err := config.InstallHandlers(http.NewServeMux(), "https://example.test")
			if failInit {
				if err == nil {
					t.Fatal("InstallHandlers succeeded; want initialization error")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if len(closed) != 0 {
					t.Fatalf("handlers closed during startup: %v", closed)
				}
				if err := shutdown.Close(); !errors.Is(err, closeErr) {
					t.Errorf("shutdown.Close = %v; want %v", err, closeErr)
				}
			}
			if want := []string{"handler", "storage"}; !reflect.DeepEqual(closed, want) {
				t.Errorf("close order = %v; want %v", closed, want)
			}
		})
	}
}
