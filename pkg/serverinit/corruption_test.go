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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go4.org/jsonconfig"

	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/serverinit"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

func TestRecoverCorruptedIndex(t *testing.T) {
	test.TLog(t)
	dbDir := filepath.Join(t.TempDir(), "corrupt.leveldb")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}

	id := shutdownTestConstructorID.Add(1)
	storageType := fmt.Sprintf("corrupt-test-index-%d", id)

	var calls int
	blobserver.RegisterStorageConstructor(storageType, func(_ blobserver.Loader, conf jsonconfig.Obj) (blobserver.Storage, error) {
		conf.OptionalBool("reindex", false)
		if err := conf.Validate(); err != nil {
			return nil, err
		}
		calls++
		if calls == 1 {
			return nil, fmt.Errorf("opening meta: %w", &sorted.CorruptedDBError{
				Path: dbDir,
				Err:  errors.New("leveldb: manifest corrupted"),
			})
		}
		return shutdownTestStorage{close: func() error { return nil }}, nil
	})

	makeConfig := func(reindex bool) *serverinit.Config {
		calls = 0
		config := serverinit.ExportNewConfigFromObj(jsonconfig.Obj{
			"auth": "none",
			"prefixes": map[string]any{
				"/storage/": map[string]any{
					"handler": "storage-" + storageType, "internal": true,
					"handlerArgs": map[string]any{
						"reindex": reindex,
					},
				},
			},
		})
		config.SetReindex(reindex)
		return config
	}

	t.Run("no reindex", func(t *testing.T) {
		shutdown, err := makeConfig(false).InstallHandlers(http.NewServeMux(), "https://example.test")
		if shutdown != nil {
			shutdown.Close()
		}
		if err == nil {
			t.Fatal("InstallHandlers succeeded; want corruption error")
		}
		if !strings.Contains(err.Error(), "corrupted") {
			t.Fatalf("error = %v; want corruption error", err)
		}
	})

	t.Run("reindex", func(t *testing.T) {
		if err := os.MkdirAll(dbDir, 0o700); err != nil {
			t.Fatal(err)
		}
		shutdown, err := makeConfig(true).InstallHandlers(http.NewServeMux(), "https://example.test")
		if err != nil {
			t.Fatalf("InstallHandlers with --reindex: %v", err)
		}
		shutdown.Close()
		if calls != 2 {
			t.Fatalf("storage constructor called %d times; want 2 (initial + retry)", calls)
		}
		if _, err := os.Stat(dbDir); !os.IsNotExist(err) {
			t.Fatalf("corrupted directory still exists after recovery; stat err = %v", err)
		}
	})
}

func TestNoRecoverCorruptedNonIndex(t *testing.T) {
	test.TLog(t)
	dbDir := filepath.Join(t.TempDir(), "corrupt-queue.leveldb")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}

	id := shutdownTestConstructorID.Add(1)
	storageType := fmt.Sprintf("corrupt-test-nonindex-%d", id)

	blobserver.RegisterStorageConstructor(storageType, func(_ blobserver.Loader, conf jsonconfig.Obj) (blobserver.Storage, error) {
		if err := conf.Validate(); err != nil {
			return nil, err
		}
		return nil, &sorted.CorruptedDBError{
			Path: dbDir,
			Err:  errors.New("leveldb: manifest corrupted"),
		}
	})

	config := serverinit.ExportNewConfigFromObj(jsonconfig.Obj{
		"auth": "none",
		"prefixes": map[string]any{
			"/storage/": map[string]any{
				"handler": "storage-" + storageType, "internal": true,
				"handlerArgs": map[string]any{},
			},
		},
	})
	config.SetReindex(true)

	shutdown, err := config.InstallHandlers(http.NewServeMux(), "https://example.test")
	if shutdown != nil {
		shutdown.Close()
	}
	if err == nil {
		t.Fatal("InstallHandlers succeeded; want corruption error for non-index storage")
	}
	if !strings.Contains(err.Error(), "corrupted") {
		t.Fatalf("error = %v; want corruption error", err)
	}
}

func TestNoRecoverCorruptedHandler(t *testing.T) {
	test.TLog(t)
	dbDir := filepath.Join(t.TempDir(), "corrupt-handler.leveldb")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}

	id := shutdownTestConstructorID.Add(1)
	handlerType := fmt.Sprintf("corrupt-test-handler-%d", id)

	blobserver.RegisterHandlerConstructor(handlerType, func(_ blobserver.Loader, conf jsonconfig.Obj) (http.Handler, error) {
		if err := conf.Validate(); err != nil {
			return nil, err
		}
		return nil, &sorted.CorruptedDBError{
			Path: dbDir,
			Err:  errors.New("leveldb: manifest corrupted"),
		}
	})

	config := serverinit.ExportNewConfigFromObj(jsonconfig.Obj{
		"auth": "none",
		"prefixes": map[string]any{
			"/handler/": map[string]any{
				"handler":     handlerType,
				"handlerArgs": map[string]any{},
			},
		},
	})
	config.SetReindex(true)

	shutdown, err := config.InstallHandlers(http.NewServeMux(), "https://example.test")
	if shutdown != nil {
		shutdown.Close()
	}
	if err == nil {
		t.Fatal("InstallHandlers succeeded; want corruption error for handler")
	}
	if !strings.Contains(err.Error(), "corrupted") {
		t.Fatalf("error = %v; want corruption error", err)
	}
}
