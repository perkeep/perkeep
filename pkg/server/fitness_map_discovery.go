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

package server

import (
	"net/url"
	"os"
	"strings"

	"perkeep.org/pkg/types/camtypes"
)

func addFitnessMapDiscovery(discovery *camtypes.UIDiscovery) {
	source, err := fitnessTileSourceFromEnv()
	if err != nil || source == nil {
		return
	}
	credit, link := fitnessMapCredit()
	discovery.FitnessMapEnabled = true
	discovery.FitnessMapProfile = fitnessMapProfile(source)
	discovery.FitnessMapAttribution = credit
	discovery.FitnessMapAttributionURL = link
}

func fitnessMapCredit() (credit, link string) {
	credit = fitnessMapAttributionText()
	link = strings.TrimSpace(os.Getenv("PERKEEP_FITNESS_TILE_ATTRIBUTION_URL"))
	u, err := url.Parse(link)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		link = "https://www.openstreetmap.org/copyright"
	}
	return credit, link
}

func fitnessMapProfile(source *fitnessTileSource) string {
	credit, link := fitnessMapCredit()
	return fitnessTileHash(source.profile() + "\x00" + credit + "\x00" + link)
}
