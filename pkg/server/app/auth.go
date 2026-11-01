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

package app

import (
	"net/http"

	"perkeep.org/pkg/auth"
)

// appAuth grants the app's existing blob/search access, not the owner's
// server-local secret access. Being scoped also keeps discovery from handing
// the app a more privileged credential if its operations change in the future.
type appAuth struct{ auth.AuthMode }

func (a appAuth) AllowedAccess(r *http.Request) auth.Operation {
	return a.AuthMode.AllowedAccess(r) & auth.OpAll
}

func (appAuth) ScopedOnly() {}
