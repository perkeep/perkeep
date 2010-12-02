package main

import (
	"bytes"
	"crypto/openpgp/armor"
	"crypto/openpgp/packet"
	"crypto/rsa"
	"crypto/sha1"
	"io/ioutil"
	"log"
)

func readOpenPGPPacketFromArmoredFileOrDie(fileName string, armorType string) (p packet.Packet) {
	data, err := ioutil.ReadFile(fileName)
	if err != nil {
		log.Exit("Cannot open '%s': %s", fileName, err)
	}

	block, _ := armor.Decode(data)
	if block == nil {
		log.Exit("cannot parse armor")
	}
	if block.Type != armorType {
		log.Exitf("bad type in '%s' (got: %s, want: %s)", fileName, block.Type, armorType)
	}
	buf := bytes.NewBuffer(block.Bytes)
	p, err = packet.ReadPacket(buf)
	if err != nil {
		log.Exitf("failed to parse packet from '%s': %s", fileName, err)
	}
	return
}

func main() {
	signedData, err := ioutil.ReadFile("signed-file")
	if err != nil {
		log.Exitf("Cannot open 'signed-file': %s", err)
	}

	p := readOpenPGPPacketFromArmoredFileOrDie("public-key", "PGP PUBLIC KEY BLOCK")
	pk, ok := p.(packet.PublicKeyPacket)
	if !ok {
		log.Exit("didn't find a public key in the public key file")
	}

	p = readOpenPGPPacketFromArmoredFileOrDie("signed-file.asc", "PGP SIGNATURE")
	sig, ok := p.(packet.SignaturePacket)
	if !ok {
		log.Exit("didn't find a signature in the signature file")
	}

	if sig.Hash != packet.HashFuncSHA1 {
		log.Exit("I only do SHA1")
	}
	if sig.SigType != packet.SigTypeBinary {
		log.Exit("I only do binary signatures")
	}

	hash := sha1.New()
	hash.Write(signedData)
	hash.Write(sig.HashSuffix)
	hashBytes := hash.Sum()

	if hashBytes[0] != sig.HashTag[0] || hashBytes[1] != sig.HashTag[1] {
		log.Exit("hash tag doesn't match")
	}

	err = rsa.VerifyPKCS1v15(&pk.PublicKey, rsa.HashSHA1, hashBytes, sig.Signature)
	if err != nil {
		log.Exitf("bad signature: %s", err)
	}

	log.Print("good signature")
}


