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

package search_test

import (
	"context"

	"perkeep.org/pkg/client"
	"perkeep.org/pkg/search"
)

// Client search methods must continue accepting the public search types even
// though the implementation imports only the lightweight protocol package.
var (
	_ search.QueryDescriber                                                                            = (*client.Client)(nil)
	_ search.GetRecentPermanoder                                                                       = (*client.Client)(nil)
	_ func(*client.Client, context.Context, *search.WithAttrRequest) (*search.WithAttrResponse, error) = (*client.Client).GetPermanodesWithAttr
	_ func(*client.Client, context.Context, *search.ClaimsRequest) (*search.ClaimsResponse, error)     = (*client.Client).GetClaims
	_ func(*client.Client, context.Context, *search.SearchQuery) ([]byte, error)                       = (*client.Client).QueryRaw
)
