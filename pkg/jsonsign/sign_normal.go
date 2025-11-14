/*
Copyright 2013 The Perkeep Authors

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

package jsonsign

import (
	"errors"
	"fmt"
	"log"
	"os"

	"golang.org/x/crypto/openpgp"
	"perkeep.org/internal/gpgagent"
	"perkeep.org/internal/pinentry"
)

func (fe *FileEntityFetcher) decryptEntity(e *openpgp.Entity) error {
	// TODO: syscall.Mlock a region and keep pass phrase in it.
	pubk := &e.PrivateKey.PublicKey
	desc := fmt.Sprintf("Need to unlock GPG key %s to use it for signing.",
		pubk.KeyIdShortString())

	conn, err := gpgagent.NewConn()
	switch err {
	case gpgagent.ErrNoAgent:
		fmt.Fprintf(os.Stderr, "Note: gpg-agent not found; resorting to on-demand password entry.\n")
	case nil:
		defer conn.Close()
		req := &gpgagent.PassphraseRequest{
			CacheKey: "camli:jsonsign:" + pubk.KeyIdShortString(),
			Prompt:   "Passphrase",
			Desc:     desc,
		}
		for range 2 {
			pass, err := conn.GetPassphrase(req)
			if err == nil {
				err = e.PrivateKey.Decrypt([]byte(pass))
				if err == nil {
					return nil
				}
				req.Error = "Passphrase failed to decrypt: " + err.Error()
				conn.RemoveFromCache(req.CacheKey)
				continue
			}
			if err == gpgagent.ErrCancel {
				return errors.New("jsonsign: failed to decrypt key; action canceled")
			}
			log.Printf("jsonsign: gpgagent: %v", err)
		}
	default:
		log.Printf("jsonsign: gpgagent: %v", err)
	}

	pinReq := &pinentry.Request{Desc: desc, Prompt: "Passphrase"}
	for range 2 {
		pass, err := pinReq.GetPIN()
		if err == nil {
			err = e.PrivateKey.Decrypt([]byte(pass))
			if err == nil {
				return nil
			}
			pinReq.Error = "Passphrase failed to decrypt: " + err.Error()
			continue
		}
		if err == pinentry.ErrCancel {
			return errors.New("jsonsign: failed to decrypt key; action canceled")
		}
		log.Printf("jsonsign: pinentry: %v", err)
	}
	return fmt.Errorf("jsonsign: failed to decrypt key %q", pubk.KeyIdShortString())
}
