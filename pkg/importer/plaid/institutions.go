/*
Copyright 2017 The Camlistore Authors

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

package plaid

type Institution int

const (
	AMEX = iota
	BBT
	BOFA
	CAPONE
	SCHWAB
	CHASE
	CITI
	FIDELITY
	NFCU
	PNC
	SUNTRUST
	TD
	US
	USAA
	WELLS
)

type InstitutionNames struct {
	DisplayName string
	CodeName    string
}

type InstitutionNameMap map[Institution]InstitutionNames

var supportedInstitutions InstitutionNameMap

func init() {
	supportedInstitutions = InstitutionNameMap{
		AMEX:     {"Amex", "amex"},
		BBT:      {"BB&T", "bbt"},
		BOFA:     {"Bank of America", "bofa"},
		CAPONE:   {"Capital One", "capone"},
		CITI:     {"Citi", "citi"},
		SCHWAB:   {"Chales Schwab", "schwab"},
		CHASE:    {"Chase", "chase"},
		FIDELITY: {"Fidelity", "fidelity"},
		NFCU:     {"Navy FCU", "nfcu"},
		PNC:      {"PNC", "pnc"},
		SUNTRUST: {"Suntrust", "suntrust"},
		TD:       {"TD Bank", "td"},
		US:       {"US Bank", "us"},
		USAA:     {"USAA", "usaa"},
		WELLS:    {"Wells Fargo", "wells"},
	}
}
