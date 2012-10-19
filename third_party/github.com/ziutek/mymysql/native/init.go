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
	if my.info.caps&_CLIENT_PROTOCOL_41 != 0 {
		readFull(pr, my.info.scramble[8:])
	}
	pr.readAll() // Skip other information
	if my.Debug {
		log.Printf(tab8s+"ProtVer=%d, ServVer=\"%s\" Status=0x%x",
			my.info.prot_ver, my.info.serv_ver, my.status,
		)
	}
	if my.info.caps&_CLIENT_PROTOCOL_41 == 0 {
		panic(OLD_PROTOCOL_ERROR)
	}
}

func (my *Conn) auth() {
	if my.Debug {
		log.Printf("[%2d <-] Authentication packet", my.seq)
	}
	flags := uint32(
		_CLIENT_PROTOCOL_41 |
			_CLIENT_LONG_PASSWORD |
			_CLIENT_LONG_FLAG |
			_CLIENT_TRANSACTIONS |
			_CLIENT_SECURE_CONN |
			_CLIENT_MULTI_STATEMENTS |
			_CLIENT_MULTI_RESULTS)
	// Reset flags not supported by server
	flags &= uint32(my.info.caps) | 0xffff0000
	scrPasswd := encryptedPasswd(my.passwd, my.info.scramble)
	pay_len := 4 + 4 + 1 + 23 + len(my.user) + 1 + 1 + len(scrPasswd)
	if len(my.dbname) > 0 {
		pay_len += len(my.dbname) + 1
		flags |= _CLIENT_CONNECT_WITH_DB
	}
	pw := my.newPktWriter(pay_len)
	writeU32(pw, flags)
	writeU32(pw, uint32(my.max_pkt_size))
	writeByte(pw, my.info.lang) // Charset number
	write(pw, make([]byte, 23)) // Filler
	writeNTS(pw, my.user)       // Username
	writeBin(pw, scrPasswd)     // Encrypted password
	if len(my.dbname) > 0 {
		writeNTS(pw, my.dbname)
	}
	if len(my.dbname) > 0 {
		pay_len += len(my.dbname) + 1
		flags |= _CLIENT_CONNECT_WITH_DB
	}
	return
}

func (my *Conn) oldPasswd() {
	if my.Debug {
		log.Printf("[%2d <-] Password packet", my.seq)
	}
	scrPasswd := encryptedOldPassword(my.passwd, my.info.scramble)
	pw := my.newPktWriter(len(scrPasswd) + 1)
	write(pw, scrPasswd)
	writeByte(pw, 0)
}
