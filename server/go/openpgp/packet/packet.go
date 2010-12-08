package packet

import (
	"fmt"
	"big"
	"crypto/openpgp/error"
	"crypto/rsa"
	"crypto/sha1"
	"io"
	"os"
)

func readHeader(r io.Reader) (tag uint8, length uint64, err os.Error) {
	var buf [4]byte
	_, err = io.ReadFull(r, buf[0:1])
	if err != nil {
		return
	}
	if buf[0] & 0x80 == 0 {
		err = error.StructuralError("tag byte does not have MSB set")
		return
	}
	if buf[0] & 0x40 == 0 {
		// Old format packet
		tag = (buf[0] & 0x3f) >> 2
		lengthType := buf[0] & 3
		if lengthType == 3 {
			err = error.Unsupported("indeterminate length packet")
			return
		}
		lengthBytes := 1 << lengthType
		_, err = io.ReadFull(r, buf[0:lengthBytes])
		if err != nil {
			return
		}
		for i := 0; i < lengthBytes; i++ {
			length <<= 8
			length |= uint64(buf[i])
		}
		return
	}

	// New format packet
	tag = buf[0] & 0x3f
	_, err = io.ReadFull(r, buf[0:1])
	if err != nil {
		return
	}
	switch {
		case buf[0] < 192:
			length = uint64(buf[0])
		case buf[0] < 224:
			length = uint64(buf[0] - 192) << 8
			_, err = io.ReadFull(r, buf[0:1])
			if err != nil {
				return
			}
			length += uint64(buf[0]) + 192
		case buf[0] < 255:
			err = error.Unsupported("chunked packet")
		default:
			_, err := io.ReadFull(r, buf[0:4])
			if err != nil {
				return
			}
			length = uint64(buf[0]) << 24 |
				 uint64(buf[1]) << 16 |
				 uint64(buf[2]) << 8 |
				 uint64(buf[3])
	}
	return
}

type Packet interface {
	Type() string
}

func ReadPacket(r io.Reader) (p Packet, err os.Error) {
	tag, length, err := readHeader(r)
	limitReader := io.LimitReader(r, int64(length))
	switch tag {
		case 2:
			p, err = readSignaturePacket(limitReader)
		case 6:
			p, err = readPublicKeyPacket(limitReader, uint16(length))
		default:
			err = error.Unsupported("unknown packet type")
	}
	return
}

type SignatureType uint8
type PublicKeyAlgorithm uint8
type HashFunction uint8

const (
	SigTypeBinary SignatureType = 0
	SigTypeText SignatureType = 1
	// Many other types omitted
)

const (
	// RFC 4880, section 9.1
	PubKeyAlgoRSA PublicKeyAlgorithm = 1
	PubKeyAlgoRSAEncryptOnly PublicKeyAlgorithm = 2
	PubKeyAlgoRSASignOnly PublicKeyAlgorithm = 3
	PubKeyAlgoElgamal PublicKeyAlgorithm = 16
	PubKeyAlgoDSA PublicKeyAlgorithm = 17
)

const (
	// RFC 4880, section 9.4
	HashFuncSHA1 = 2
)

type SignaturePacket struct {
	SigType SignatureType
	PubKeyAlgo PublicKeyAlgorithm
	Hash HashFunction
	HashSuffix []byte
	HashTag [2]byte
	CreationTime uint32
	Signature []byte
}

func (s SignaturePacket) Type() string {
	return "signature"
}

func readSignaturePacket(r io.Reader) (sig SignaturePacket, err os.Error) {
	// RFC 4880, section 5.2.3
	var buf [5]byte
	_, err = io.ReadFull(r, buf[:1])
	if err != nil {
		return
	}
	if buf[0] != 4 {
		err = error.Unsupported("signature packet version")
		return
	}

	_, err = io.ReadFull(r, buf[:5])
	if err != nil {
		return
	}
	sig.SigType = SignatureType(buf[0])
	sig.PubKeyAlgo = PublicKeyAlgorithm(buf[1])
	switch sig.PubKeyAlgo {
		case PubKeyAlgoRSA, PubKeyAlgoRSASignOnly:
		default:
			err = error.Unsupported("public key algorithm")
			return
	}
	sig.Hash = HashFunction(buf[2])
	hashedSubpacketsLength := int(buf[3]) << 8 | int(buf[4])
	l := 6 + hashedSubpacketsLength
	sig.HashSuffix = make([]byte, l + 6)
	sig.HashSuffix[0] = 4
	copy(sig.HashSuffix[1:], buf[:5])
	hashedSubpackets := sig.HashSuffix[6:l]
	_, err = io.ReadFull(r, hashedSubpackets)
	if err != nil {
		return
	}
	// See RFC 4880, section 5.2.4
	trailer := sig.HashSuffix[l:]
	trailer[0] = 4
	trailer[1] = 0xff
	trailer[2] = uint8(l >> 24)
	trailer[3] = uint8(l >> 16)
	trailer[4] = uint8(l >> 8)
	trailer[5] = uint8(l)

	err = parseSignatureSubpackets(&sig, hashedSubpackets, true)
	if err != nil {
		return
	}

	_, err = io.ReadFull(r, buf[:2])
	if err != nil {
		return
	}
	unhashedSubpacketsLength := int(buf[0]) << 8 | int(buf[1])
	unhashedSubpackets := make([]byte, unhashedSubpacketsLength)
	_, err = io.ReadFull(r, unhashedSubpackets)
	if err != nil {
		return
	}
	err = parseSignatureSubpackets(&sig, unhashedSubpackets, false)
	if err != nil {
		return
	}

	_, err = io.ReadFull(r, sig.HashTag[:2])
	if err != nil {
		return
	}

	// We have already checked that the public key algorithm is RSA.
	sig.Signature, _, err = readMPI(r)
	return
}

func readMPI(r io.Reader) (mpi []byte, hdr [2]byte, err os.Error) {
	_, err = io.ReadFull(r, hdr[0:])
	if err != nil {
		return
	}
	numBits := int(hdr[0]) << 8 | int(hdr[1])
	numBytes := (numBits + 7) / 8
	mpi = make([]byte, numBytes)
	_, err = io.ReadFull(r, mpi)
	return
}

func parseSignatureSubpackets(sig *SignaturePacket, subpackets []byte, isHashed bool) (err os.Error) {
	for len(subpackets) > 0 {
		subpackets, err = parseSignatureSubpacket(sig, subpackets, isHashed)
		if err != nil {
			return
		}
	}

	if sig.CreationTime == 0 {
		err = error.StructuralError("no creation time in signature")
	}

	return
}

func parseSignatureSubpacket(sig *SignaturePacket, subpacket []byte, isHashed bool) (rest []byte, err os.Error) {
	// RFC 4880, section 5.2.3.1
	var length uint32
	switch {
		case subpacket[0] < 192:
			length = uint32(subpacket[0])
			subpacket = subpacket[1:]
		case subpacket[0] < 255:
			if len(subpacket) < 2 {
				goto Truncated
			}
			length = uint32(subpacket[0] - 192) << 8 + uint32(subpacket[1]) + 192
			subpacket = subpacket[2:]
		default:
			if len(subpacket) < 5 {
				goto Truncated
			}
			length = uint32(subpacket[1]) << 24 |
				 uint32(subpacket[2]) << 16 |
				 uint32(subpacket[3]) << 8 |
				 uint32(subpacket[4])
			subpacket = subpacket[5:]
	}
	if length < uint32(len(subpacket)) {
		goto Truncated
	}
	rest = subpacket[length:]
	subpacket = subpacket[:length]
	if len(subpacket) == 0 {
		err = error.StructuralError("zero length signature subpacket")
		return
	}
	packetType := subpacket[0] & 0x7f
	isCritial := subpacket[0] & 0x80 == 0x80
	subpacket = subpacket[1:]
	switch packetType {
		case 2:
			if !isHashed {
				err = error.StructuralError("signature creation time in non-hashed area")
				return
			}
			if len(subpacket) != 4 {
				err = error.StructuralError("signature creation time not four bytes")
				return
			}
			sig.CreationTime = uint32(subpacket[0]) << 24 |
					   uint32(subpacket[1]) << 16 |
					   uint32(subpacket[2]) << 8 |
					   uint32(subpacket[3])
		default:
			if isCritial {
				err = error.Unsupported("unknown critical signature subpacket")
				return
			}
	}
	return

 Truncated:
	err = error.StructuralError("signature subpacket truncated")
	return
}

type PublicKeyPacket struct {
	CreationTime uint32
	PubKeyAlgo   PublicKeyAlgorithm
	PublicKey    rsa.PublicKey
	Fingerprint  []byte
}

func (pk PublicKeyPacket) Type() string {
	return "public key"
}

func (pk PublicKeyPacket) FingerprintString() string {
	return fmt.Sprintf("%X", pk.Fingerprint)
}

func (pk PublicKeyPacket) KeyIdString() string {
	return fmt.Sprintf("%X", pk.Fingerprint[len(pk.Fingerprint)-4:])
}

func readPublicKeyPacket(r io.Reader, length uint16) (pk PublicKeyPacket, err os.Error) {
	// RFC 4880, section 5.5.2
	var buf [6]byte
	_, err = io.ReadFull(r, buf[0:])
	if err != nil {
		return
	}
	if buf[0] != 4 {
		err = error.Unsupported("public key version")
	}

	// RFC 4880, section 12.2
	fprint := sha1.New()
	fprint.Write([]byte{'\x99', uint8(length >> 8),
		uint8(length & 0xff)})
	fprint.Write(buf[0:6]) // version, timestamp, algorithm

	pk.CreationTime = uint32(buf[1]) << 24 |
			  uint32(buf[2]) << 16 |
			  uint32(buf[3]) << 8 |
			  uint32(buf[4])
	pk.PubKeyAlgo = PublicKeyAlgorithm(buf[5])
	switch pk.PubKeyAlgo {
		case PubKeyAlgoRSA, PubKeyAlgoRSAEncryptOnly, PubKeyAlgoRSASignOnly:
		default:
			err = error.Unsupported("public key type")
			return
	}

	nBytes, mpiHdr, err := readMPI(r)
	if err != nil {
		return
	}
	fprint.Write(mpiHdr[:])
	fprint.Write(nBytes)

	eBytes, mpiHdr, err := readMPI(r)
	if err != nil {
		return
	}
	fprint.Write(mpiHdr[:])
	fprint.Write(eBytes)

	if len(eBytes) > 3 {
		err = error.Unsupported("large public exponent")
		return
	}
	pk.PublicKey.E = 0
	for i := 0; i < len(eBytes); i++ {
		pk.PublicKey.E <<= 8
		pk.PublicKey.E |= int(eBytes[i])
	}
	pk.PublicKey.N = (new(big.Int)).SetBytes(nBytes)
	pk.Fingerprint = fprint.Sum()
	return
}
