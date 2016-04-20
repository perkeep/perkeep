// mgo - MongoDB driver for Go
//
// Copyright (c) 2010-2012 - Gustavo Niemeyer <gustavo@niemeyer.net>
//
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
//
// 1. Redistributions of source code must retain the above copyright notice, this
//    list of conditions and the following disclaimer.
// 2. Redistributions in binary form must reproduce the above copyright notice,
//    this list of conditions and the following disclaimer in the documentation
//    and/or other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
// ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
// WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
// DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE LIABLE FOR
// ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES
// (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES;
// LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND
// ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package mgo

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"labix.org/v2/mgo/bson"
	"sync"
)

type authInfo struct {
	db, user, pass string
}

type authCmd struct {
	Authenticate     int
	Nonce, User, Key string
}

type authResult struct {
	ErrMsg string
	Ok     bool
}

type getNonceCmd struct {
	GetNonce int
}

type getNonceResult struct {
	Nonce string
	Err   string "$err"
	Code  int
}

type logoutCmd struct {
	Logout int
}

func (socket *mongoSocket) getNonce() (nonce string, err error) {
	socket.Lock()
	for socket.cachedNonce == "" && socket.dead == nil {
		debugf("Socket %p to %s: waiting for nonce", socket, socket.addr)
		socket.gotNonce.Wait()
	}
	if socket.cachedNonce == "mongos" {
		socket.Unlock()
		return "", errors.New("Can't authenticate with mongos; see http://j.mp/mongos-auth")
	}
	debugf("Socket %p to %s: got nonce", socket, socket.addr)
	nonce, err = socket.cachedNonce, socket.dead
	socket.cachedNonce = ""
	socket.Unlock()
	if err != nil {
		nonce = ""
	}
	return
}

func (socket *mongoSocket) resetNonce() {
	debugf("Socket %p to %s: requesting a new nonce", socket, socket.addr)
	op := &queryOp{}
	op.query = &getNonceCmd{GetNonce: 1}
	op.collection = "admin.$cmd"
	op.limit = -1
	op.replyFunc = func(err error, reply *replyOp, docNum int, docData []byte) {
		if err != nil {
			socket.kill(errors.New("getNonce: "+err.Error()), true)
			return
		}
		result := &getNonceResult{}
		err = bson.Unmarshal(docData, &result)
		if err != nil {
			socket.kill(errors.New("Failed to unmarshal nonce: "+err.Error()), true)
			return
		}
		debugf("Socket %p to %s: nonce unmarshalled: %#v", socket, socket.addr, result)
		if result.Code == 13390 {
			// mongos doesn't yet support auth (see http://j.mp/mongos-auth)
			result.Nonce = "mongos"
		} else if result.Nonce == "" {
			var msg string
			if result.Err != "" {
				msg = fmt.Sprintf("Got an empty nonce: %s (%d)", result.Err, result.Code)
			} else {
				msg = "Got an empty nonce"
			}
			socket.kill(errors.New(msg), true)
			return
		}
		socket.Lock()
		if socket.cachedNonce != "" {
			socket.Unlock()
			panic("resetNonce: nonce already cached")
		}
		socket.cachedNonce = result.Nonce
		socket.gotNonce.Signal()
		socket.Unlock()
	}
	err := socket.Query(op)
	if err != nil {
		socket.kill(errors.New("resetNonce: "+err.Error()), true)
	}
}

func (socket *mongoSocket) Login(db string, user string, pass string) error {
	socket.Lock()
	for _, a := range socket.auth {
		if a.db == db && a.user == user && a.pass == pass {
			debugf("Socket %p to %s: login: db=%q user=%q (already logged in)", socket, socket.addr, db, user)
			socket.Unlock()
			return nil
		}
	}
	if auth, found := socket.dropLogout(db, user, pass); found {
		debugf("Socket %p to %s: login: db=%q user=%q (cached)", socket, socket.addr, db, user)
		socket.auth = append(socket.auth, auth)
		socket.Unlock()
		return nil
	}
	socket.Unlock()

	debugf("Socket %p to %s: login: db=%q user=%q", socket, socket.addr, db, user)

	// Note that this only works properly because this function is
	// synchronous, which means the nonce won't get reset while we're
	// using it and any other login requests will block waiting for a
	// new nonce provided in the defer call below.
	nonce, err := socket.getNonce()
	if err != nil {
		return err
	}
	defer socket.resetNonce()

	psum := md5.New()
	psum.Write([]byte(user + ":mongo:" + pass))

	ksum := md5.New()
	ksum.Write([]byte(nonce + user))
	ksum.Write([]byte(hex.EncodeToString(psum.Sum(nil))))

	key := hex.EncodeToString(ksum.Sum(nil))

	cmd := authCmd{Authenticate: 1, User: user, Nonce: nonce, Key: key}

	var mutex sync.Mutex
	var replyErr error
	mutex.Lock()

	op := queryOp{}
	op.query = &cmd
	op.collection = db + ".$cmd"
	op.limit = -1
	op.replyFunc = func(err error, reply *replyOp, docNum int, docData []byte) {
		defer mutex.Unlock()

		if err != nil {
			replyErr = err
			return
		}

		// Must handle this within the read loop for the socket, so
		// that concurrent login requests are properly ordered.
		result := &authResult{}
		err = bson.Unmarshal(docData, result)
		if err != nil {
			replyErr = err
			return
		}
		if !result.Ok {
			replyErr = errors.New(result.ErrMsg)
		}

		socket.Lock()
		socket.dropAuth(db)
		socket.auth = append(socket.auth, authInfo{db, user, pass})
		socket.Unlock()
	}

	err = socket.Query(&op)
	if err != nil {
		return err
	}
	mutex.Lock() // Wait.
	if replyErr != nil {
		debugf("Socket %p to %s: login error: %s", socket, socket.addr, replyErr)
	} else {
		debugf("Socket %p to %s: login successful", socket, socket.addr)
	}
	return replyErr
}

func (socket *mongoSocket) Logout(db string) {
	socket.Lock()
	auth, found := socket.dropAuth(db)
	if found {
		debugf("Socket %p to %s: logout: db=%q (flagged)", socket, socket.addr, db)
		socket.logout = append(socket.logout, auth)
	}
	socket.Unlock()
}

func (socket *mongoSocket) LogoutAll() {
	socket.Lock()
	if l := len(socket.auth); l > 0 {
		debugf("Socket %p to %s: logout all (flagged %d)", socket, socket.addr, l)
		socket.logout = append(socket.logout, socket.auth...)
		socket.auth = socket.auth[0:0]
	}
	socket.Unlock()
}

func (socket *mongoSocket) flushLogout() (ops []interface{}) {
	socket.Lock()
	if l := len(socket.logout); l > 0 {
		debugf("Socket %p to %s: logout all (flushing %d)", socket, socket.addr, l)
		for i := 0; i != l; i++ {
			op := queryOp{}
			op.query = &logoutCmd{1}
			op.collection = socket.logout[i].db + ".$cmd"
			op.limit = -1
			ops = append(ops, &op)
		}
		socket.logout = socket.logout[0:0]
	}
	socket.Unlock()
	return
}

func (socket *mongoSocket) dropAuth(db string) (auth authInfo, found bool) {
	for i, a := range socket.auth {
		if a.db == db {
			copy(socket.auth[i:], socket.auth[i+1:])
			socket.auth = socket.auth[:len(socket.auth)-1]
			return a, true
		}
	}
	return auth, false
}

func (socket *mongoSocket) dropLogout(db, user, pass string) (auth authInfo, found bool) {
	for i, a := range socket.logout {
		if a.db == db && a.user == user && a.pass == pass {
			copy(socket.logout[i:], socket.logout[i+1:])
			socket.logout = socket.logout[:len(socket.logout)-1]
			return a, true
		}
	}
	return auth, false
}
