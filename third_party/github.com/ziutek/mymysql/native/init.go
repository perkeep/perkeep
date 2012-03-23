package native

import (
	"log"
)

func (my *Conn) init() {
	my.seq = 0 // Reset sequence number, mainly for reconnect
	if my.Debug {
		log.Printf("[%2d ->] Init packet:", my.seq)
	}
	pr := my.newPktReader()
	my.info.scramble = make([]byte, 20)

	my.info.prot_ver = readByte(pr)
	my.info.serv_ver = readNTS(pr)
	my.info.thr_id = readU32(pr)
	readFull(pr, my.info.scramble[0:8])
	read(pr, 1)
	my.info.caps = readU16(pr)
	my.info.lang = readByte(pr)
	my.status = readU16(pr)
	read(pr, 13)
	readFull(pr, my.info.scramble[8:])
	// Skip other information
	pr.readAll()

	if my.Debug {
		log.Printf(tab8s+"ProtVer=%d, ServVer=\"%s\" Status=0x%x",
			my.info.prot_ver, my.info.serv_ver, my.status,
		)
	}
}

func (my *Conn) auth() {
	if my.Debug {
		log.Printf("[%2d <-] Authentication packet", my.seq)
	}
	pay_len := 4 + 4 + 1 + 23 + len(my.user) + 1 + 1 + len(my.info.scramble)
	flags := uint32(
		_CLIENT_PROTOCOL_41 |
			_CLIENT_LONG_PASSWORD |
			_CLIENT_SECURE_CONN |
			_CLIENT_MULTI_STATEMENTS |
			_CLIENT_MULTI_RESULTS |
			_CLIENT_TRANSACTIONS,
	)
	if len(my.dbname) > 0 {
		pay_len += len(my.dbname) + 1
		flags |= _CLIENT_CONNECT_WITH_DB
	}
	encr_passwd := my.encryptedPasswd()

	pw := my.newPktWriter(pay_len)
	writeU32(pw, flags)
	writeU32(pw, uint32(my.max_pkt_size))
	writeByte(pw, my.info.lang) // Charset number
	write(pw, make([]byte, 23)) // Filler
	writeNTS(pw, my.user)       // Username
	writeBin(pw, encr_passwd)   // Encrypted password
	if len(my.dbname) > 0 {
		writeNTS(pw, my.dbname)
	}
	return
}
