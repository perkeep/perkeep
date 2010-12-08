package main

import (
	"bytes"
	"fmt"
	"flag"
	"os"
	"io/ioutil"
	"crypto/openpgp/armor"
        "crypto/openpgp/packet"
)

var flagPubKeyDir *string = flag.String("pubkey-dir", "test/pubkey-blobs",
	"Temporary development hack; directory to dig-xxxx.camli public keys.")

func openArmoredPublicKeyFile(fileName string) (*packet.PublicKeyPacket, os.Error) {
	data, err := ioutil.ReadFile(fileName)
	if err != nil {
		return nil, os.NewError(fmt.Sprintf("Error reading public key file: %v", err))
	}

	block, _ := armor.Decode(data)
	if block == nil {
		return nil, os.NewError("Couldn't find PGP block in public key file")
	}
	if block.Type != "PGP PUBLIC KEY BLOCK" {
		return nil, os.NewError("Invalid public key blob.")
	}
	buf := bytes.NewBuffer(block.Bytes)
	p, err := packet.ReadPacket(buf)
	if err != nil {
		return nil, os.NewError(fmt.Sprintf("Invalid public key blob: %v", err))
	}

	pk, ok := p.(packet.PublicKeyPacket)
	if !ok {
		return nil, os.NewError(fmt.Sprintf("Invalid public key blob; not a public key packet"))
	}
	return &pk, nil
}

