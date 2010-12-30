package main

import (
	"bytes"
	"crypto/openpgp/armor"
	"crypto/openpgp/packet"
//	"crypto/rsa"
//	"crypto/sha1"
	"io/ioutil"
	"log"
	"flag"
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

var flagFile *string = flag.String("file", "", "filename of public key")

func main() {
	flag.Parse()

	p := readOpenPGPPacketFromArmoredFileOrDie(*flagFile, "PGP PUBLIC KEY BLOCK")
	pk, ok := p.(packet.PublicKeyPacket)
	if !ok {
		log.Exit("didn't find a public key in the public key file")
	}

	log.Printf("packet: %v", pk)
	log.Printf("Fingerprint: %s", pk.FingerprintString())
	log.Printf("     Key ID: %s", pk.KeyIdString())
}


