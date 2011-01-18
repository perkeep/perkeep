package jsonsign

import (
	"bytes"
	"fmt"
	"os"
	"io"
	"io/ioutil"
	"crypto/openpgp/armor"
        "crypto/openpgp/packet"
	"strings"
)

const publicKeyMaxSize = 256 * 1024

func VerifyPublicKeyFile(file, keyid string) (bool, os.Error) {
	f, err := os.Open(file, os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}

	key, err := openArmoredPublicKeyFile(f)
	if err != nil {
		return false, err
	}
	if key.KeyIdString() != strings.ToUpper(keyid) {
		return false, os.NewError(fmt.Sprintf("Key in file %q has id %q; expected %q",
			file, key.KeyIdString(), keyid))
	}
	return true, nil
}

func openArmoredPublicKeyFile(reader io.ReadCloser) (*packet.PublicKeyPacket, os.Error) {
	defer reader.Close()

	var lr = io.LimitReader(reader, publicKeyMaxSize)
	data, err := ioutil.ReadAll(lr)
	if err != nil {
		return nil, os.NewError(fmt.Sprintf("Error reading public key file: %v", err))
	}
	if len(data) == publicKeyMaxSize {
		return nil, os.NewError(fmt.Sprintf("Public key blob is too large"))
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
