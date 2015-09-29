/*
Copyright 2015 The Camlistore Authors

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

package camtypes

// VerifyResponse is the JSON response for a signature verification request.
type VerifyResponse struct {
	// SignatureValid is true if the signature is valid.
	SignatureValid bool `json:"signatureValid"`
	// ErrorMessage contains the error that occurred, if any.
	ErrorMessage string `json:"errorMessage,omitempty"`
	// SignerKeyId is the ID of the signing key.
	SignerKeyId string `json:"signerKeyId,omitempty"`
	// VerifiedData contains the JSON values from the payload that we signed.
	VerifiedData map[string]interface{} `json:"verifiedData,omitempty"`
}
