/*
Copyright 2016 The Camlistore Authors

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

// Package gpgchallenge provides a Client and a Server so that a Client can
// prove ownership of an IP address by solving a GPG challenge sent by the Server
// at the claimed IP.
// The protocol is as follows:
//
// - The Client GETs a random token from the server, at the /token endpoint, and signs
// that token with its GPG private key (armor detached signature).
//
// - When it is ready[*], the client POSTs an application/x-www-form-urlencoded over
// HTTPS to the server, at the /claim endpoint. It sends the following URL-encoded
// values as the request body: its armor encoded public key as "pubkey", the IP
// address it's claiming as "challengeIP", the token it got from the server as "token",
// and the signature for the token as "signature".
//
// - The Server receives the claim. It verifies that the token (nonce) is indeed one that
// it had generated. It parses the client's public key. It verifies with that
// public key that the sent signature matches the token. The serve ACKs to the client.
//
// - The Server generates a random token, and POSTs it to the challenged IP
// (over HTTPS, with certificate verification disabled) at the /challenge endpoint.
//
// - The Client receives the random token, signs it (armored detached
// signature), and sends the signature as a reply.
//
// - The Server receives the signed token and verifies it with the Client's
// public key.
//
// - At this point, the challenge is successful, so the Server performs the
// action registered through the OnSuccess function.
//
// - The Server sends a last message to the Client at the /ack endpoint,
// depending on the result of the OnSuccess action. "ACK" if it was successful, the
// error message otherwise.
//
// [*]As the Server connects to the Client to challenge it, the Client must obviously
// have a way, which does not need to be described by the protocol, to listen to and
// accept these connections.
package gpgchallenge // import "camlistore.org/pkg/gpgchallenge"

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/armor"
	"golang.org/x/crypto/openpgp/packet"
	"golang.org/x/net/context"
	"golang.org/x/time/rate"

	"go4.org/wkfs"
)

// ClientChallengedPort is the port that the client will be challenged on by
// the server.
var ClientChallengedPort = 443

// SNISuffix is the Server Name Indication prefix used when dialing the
// client's handler. The SNI is challengeIP+SNISuffix.
const SNISuffix = "-gpgchallenge"

const (
	clientEndPointChallenge = "challenge"
	clientEndPointAck       = "ack"
	clientEndPointReady     = "ready" // not part of the protocol, just to check if client has a listener
	serverEndPointToken     = "token"
	serverEndPointChallenge = "claim"
	// clientHandlerPrefix is the URL path prefix for all the client endpoints.
	clientHandlerPrefix = "/.well-known/camlistore/gpgchallenge/"

	nonceValidity  = 10 * time.Second
	spamDelay      = 5 * time.Second // any repeated attempt under this delay is considered as spam
	forgetSeen     = time.Minute     // anyone being quiet for that long is taken off the "potential spammer" list
	queriesRate    = 10              // max concurrent (non-whitelisted) clients
	minKeySize     = 2048            // in bits. to force potential attackers to generate GPG keys at least this expensive.
	requestTimeout = 3 * time.Second // so a client does not make use create many long-lived connections
)

// Server sends a challenge when a client that wants to claim ownership of an IP
// address requests so. Server runs OnSuccess when the challenge is successful.
type Server struct {
	// OnSuccess is user-defined, and it is run by the server upon
	// successuful verification of the client's challenge. Identity is the
	// short form of the client's public key's fingerprint in capital hex.
	// Address is the IP address that the client was claiming.
	OnSuccess func(identity, address string) error

	once sync.Once // for initializing all the fields below in serverInit

	// keyHMAC is the key for generating with HMAC, the random tokens for
	// the clients. Each token is: message-HEX(HMAC(message)) , with message
	// being the current unix time. This format allows the server to verify a
	// token it gets back from the client was indeed generated by the server.
	keyHMAC []byte

	nonceUsedMu sync.Mutex
	nonceUsed   map[string]bool // whether such a nonce has already been sent back by the client

	// All of the fields below are for rate-limiting or attacks suppression.
	limiter *rate.Limiter

	whiteListMu sync.Mutex
	// whiteList contains clients which already successfully challenged us,
	// and therefore are not subject to any rate-limiting. keyed by "keyID-IP".
	whiteList map[string]struct{}

	keyIDSeenMu sync.Mutex
	keyIDSeen   map[string]time.Time // last time we saw a keyID, for rate-limiting purposes.

	IPSeenMu sync.Mutex
	IPSeen   map[string]time.Time // last time we saw a claimedIP, for rate-limiting purposes.
}

func (cs *Server) serverInit() error {
	nonce, err := genNonce()
	if err != nil {
		return fmt.Errorf("error generating key for hmac: %v")
	}
	cs.keyHMAC = []byte(nonce)
	cs.nonceUsed = make(map[string]bool)
	cs.limiter = rate.NewLimiter(queriesRate, 1)
	cs.whiteList = make(map[string]struct{})
	cs.keyIDSeen = make(map[string]time.Time)
	cs.IPSeen = make(map[string]time.Time)
	return nil
}

func (cs *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cs.once.Do(func() {
		if err := cs.serverInit(); err != nil {
			panic(fmt.Sprintf("Could not initialize server: %v", err))
		}
	})
	if r.URL.Path == "/"+serverEndPointToken {
		cs.handleNonce(w, r)
		return
	}
	if r.URL.Path == "/"+serverEndPointChallenge {
		cs.handleClaim(w, r)
		return
	}
	http.Error(w, "nope", 404)
}

// handleNonce replies with a nonce. The nonce format is:
// message-HexOf(HMAC(message)), where message is the current unix time in seconds.
func (cs *Server) handleNonce(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "not a GET", http.StatusMethodNotAllowed)
		return
	}

	mac := hmac.New(sha256.New, cs.keyHMAC)
	message := fmt.Sprintf("%d", time.Now().Unix())
	mac.Write([]byte(message))
	messageHash := string(mac.Sum(nil))
	nonce := fmt.Sprintf("%s-%x", message, messageHash)
	if _, err := io.Copy(w, strings.NewReader(nonce)); err != nil {
		log.Printf("error sending nonce: %v", err)
		return
	}
}

func (cs *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "not a POST", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not parse claim form", 500)
		return
	}
	pks := r.Form.Get("pubkey")
	if len(pks) == 0 {
		http.Error(w, "pubkey value not found in form", http.StatusBadRequest)
		return
	}
	claimedIP := r.Form.Get("challengeIP")
	if len(claimedIP) == 0 {
		http.Error(w, "claimedIP value not found in form", http.StatusBadRequest)
		return
	}
	token := r.Form.Get("token")
	if len(token) == 0 {
		http.Error(w, "token value not found in form", http.StatusBadRequest)
		return
	}
	tokenSig := r.Form.Get("signature")
	if len(tokenSig) == 0 {
		http.Error(w, "signature value not found in form", http.StatusBadRequest)
		return
	}

	if err := cs.validateToken(token); err != nil {
		http.Error(w, "invalid token", http.StatusBadRequest)
		log.Printf("Error validating token: %v", err)
		return
	}
	pk, err := parsePubKey(strings.NewReader(pks))
	if err != nil {
		http.Error(w, "invalid public key", http.StatusBadRequest)
		log.Printf("Error parsing client public key: %v", err)
		return
	}

	keySize, err := pk.BitLength()
	if err != nil {
		http.Error(w, "could not check key size", 500)
		log.Printf("could not check key size: %v", err)
		return
	}
	if keySize < minKeySize {
		http.Error(w, fmt.Sprintf("minimum key size is %d bits", minKeySize), http.StatusBadRequest)
		return
	}

	if err := cs.validateTokenSignature(pk, token, tokenSig); err != nil {
		http.Error(w, "invalid token signature", http.StatusBadRequest)
		log.Printf("Error validating token signature: %v", err)
		return
	}

	// Verify claimedIP looks ok
	ip := net.ParseIP(claimedIP)
	if ip == nil {
		http.Error(w, "nope", http.StatusBadRequest)
		log.Printf("%q does not look like a valid IP address", claimedIP)
		return
	}
	if !ip.IsGlobalUnicast() {
		http.Error(w, "nope", http.StatusBadRequest)
		log.Printf("%q does not look like a nice IP", claimedIP)
		return
	}

	keyID := pk.KeyIdShortString()
	if isSpammer := cs.rateLimit(keyID, claimedIP); isSpammer {
		http.Error(w, "don't be a spammer", http.StatusTooManyRequests)
		return
	}

	// ACK to the client
	w.WriteHeader(http.StatusNoContent)

	nonce, err := genNonce()
	if err != nil {
		log.Print(err)
		return
	}

	tr := &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig: &tls.Config{
			ServerName:         claimedIP + SNISuffix,
			InsecureSkipVerify: true,
		},
	}
	cl := &http.Client{
		Transport: tr,
		Timeout:   requestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := cl.Post(fmt.Sprintf("https://%s:%d%s%s", claimedIP, ClientChallengedPort, clientHandlerPrefix, clientEndPointChallenge),
		"text/plain", strings.NewReader(nonce))
	if err != nil {
		log.Printf("Error while sending the challenge to the client: %v", err)
		return
	}
	defer resp.Body.Close()

	sig, err := cs.receiveSignedNonce(resp.Body)
	if err != nil {
		log.Printf("Error reading signed token: %v", err)
		return
	}

	hash := sig.Hash.New()
	hash.Write([]byte(nonce))
	if err := pk.VerifySignature(hash, sig); err != nil {
		log.Printf("Error verifying token signature: %v", err)
		return
	}
	// Client is in the clear, so we add them to the whitelist for next time
	// TODO(mpl): unbounded for now, but it would be easy to e.g. keep the
	// time as value, and regularly remove very old entries. Or use a sized
	// cache. etc.
	cs.whiteListMu.Lock()
	cs.whiteList[keyID+"-"+claimedIP] = struct{}{}
	cs.whiteListMu.Unlock()

	ackMessage := "ACK"
	if err := cs.OnSuccess(pk.KeyIdShortString(), claimedIP); err != nil {
		ackMessage = fmt.Sprintf("challenge successful, but could not perform operation: %v", err)
	}

	resp, err = cl.Post(fmt.Sprintf("https://%s:%d%s%s", claimedIP, ClientChallengedPort, clientHandlerPrefix, clientEndPointAck),
		"text/plain", strings.NewReader(ackMessage))
	if err != nil {
		log.Printf("Error sending closing message: %v", err)
		return
	}
	resp.Body.Close()
}

// rateLimit uses the cs.limiter to make sure that any client that hasn't
// previously successfully challenged us is rate limited. It also keeps track of
// clients that haven't successfully challenged, and it returns true if such a
// client should be considered a spammer.
func (cs *Server) rateLimit(keyID, claimedIP string) (isSpammer bool) {
	cs.whiteListMu.Lock()
	if _, ok := cs.whiteList[keyID+"-"+claimedIP]; ok {
		cs.whiteListMu.Unlock()
		return false
	}
	cs.whiteListMu.Unlock()
	// If they haven't successfully challenged us before, they look suspicious.
	cs.keyIDSeenMu.Lock()
	lastSeen, ok := cs.keyIDSeen[keyID]
	// always keep track of the last time we saw them
	cs.keyIDSeen[keyID] = time.Now()
	cs.keyIDSeenMu.Unlock()
	time.AfterFunc(forgetSeen, func() {
		// but everyone get a clean slate after a minute of being quiet
		cs.keyIDSeenMu.Lock()
		delete(cs.keyIDSeen, keyID)
		cs.keyIDSeenMu.Unlock()
	})
	if ok {
		// if we've seen their keyID before, they look even more suspicious, so investigate.
		if lastSeen.Add(spamDelay).After(time.Now()) {
			// we kick them out if we saw them less than 5 seconds ago.
			return true
		}
	}
	cs.IPSeenMu.Lock()
	lastSeen, ok = cs.IPSeen[claimedIP]
	// always keep track of the last time we saw them
	cs.IPSeen[claimedIP] = time.Now()
	cs.IPSeenMu.Unlock()
	time.AfterFunc(forgetSeen, func() {
		// but everyone get a clean slate after a minute of being quiet
		cs.IPSeenMu.Lock()
		delete(cs.IPSeen, claimedIP)
		cs.IPSeenMu.Unlock()
	})
	if ok {
		// if we've seen their IP before, they look even more suspicious, so investigate.
		if lastSeen.Add(spamDelay).After(time.Now()) {
			// we kick them out if we saw them less than 5 seconds ago.
			return true
		}
	}
	// global rate limit that applies to all strangers at the same time
	cs.limiter.Wait(context.Background())
	return false
}

func genNonce() (string, error) {
	buf := make([]byte, 20)
	if n, err := rand.Read(buf); err != nil || n != len(buf) {
		return "", fmt.Errorf("failed to generate random nonce: %v", err)
	}
	return fmt.Sprintf("%x", buf), nil
}

func (cs Server) validateToken(token string) error {
	// Check the token is one of ours, and not too old
	parts := strings.Split(token, "-")
	if len(parts) != 2 {
		return fmt.Errorf("client sent back an invalid token")
	}
	nonce := parts[0]
	nonceTimeSeconds, err := strconv.ParseInt(nonce, 10, 64)
	if err != nil {
		return fmt.Errorf("time in nonce could not be parsed: %v", err)
	}
	nonceTime := time.Unix(nonceTimeSeconds, 0)
	if nonceTime.Add(nonceValidity).Before(time.Now()) {
		return fmt.Errorf("client sent back an expired nonce")
	}
	mac := hmac.New(sha256.New, cs.keyHMAC)
	mac.Write([]byte(nonce))
	expectedMAC, err := hex.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("could not decode HMAC %q in nonce: %v", parts[1], err)
	}
	if !hmac.Equal(mac.Sum(nil), expectedMAC) {
		return fmt.Errorf("client sent back a nonce we did not generate")
	}

	cs.nonceUsedMu.Lock()
	if used, _ := cs.nonceUsed[nonce]; used {
		log.Printf("nonce %q has already been received", nonce)
		return nil
	}
	cs.nonceUsed[nonce] = true
	cs.nonceUsedMu.Unlock()
	time.AfterFunc(nonceValidity, func() {
		cs.nonceUsedMu.Lock()
		defer cs.nonceUsedMu.Unlock()
		delete(cs.nonceUsed, nonce)
	})
	return nil
}

func (cs Server) validateTokenSignature(pk *packet.PublicKey, token, tokenSig string) error {
	sig, err := cs.receiveSignedNonce(strings.NewReader(tokenSig))
	if err != nil {
		return err
	}
	hash := sig.Hash.New()
	hash.Write([]byte(token))
	return pk.VerifySignature(hash, sig)
}

func parsePubKey(r io.Reader) (*packet.PublicKey, error) {
	block, _ := armor.Decode(r)
	if block == nil {
		return nil, errors.New("can't parse armor")
	}
	var p packet.Packet
	var err error
	p, err = packet.Read(block.Body)
	if err != nil {
		return nil, err
	}
	pk, ok := p.(*packet.PublicKey)
	if !ok {
		return nil, errors.New("PGP packet isn't a public key")
	}
	return pk, nil
}

func (cs Server) receiveSignedNonce(r io.Reader) (*packet.Signature, error) {
	block, _ := armor.Decode(r)
	if block == nil {
		return nil, errors.New("can't parse armor")
	}
	var p packet.Packet
	var err error
	p, err = packet.Read(block.Body)
	if err != nil {
		return nil, err
	}
	sig, ok := p.(*packet.Signature)
	if !ok {
		return nil, errors.New("PGP packet isn't a signature packet")
	}
	if sig.Hash != crypto.SHA1 && sig.Hash != crypto.SHA256 {
		return nil, errors.New("can only verify SHA1 or SHA256 signatures")
	}
	if sig.SigType != packet.SigTypeBinary {
		return nil, errors.New("can only verify binary signatures")
	}

	return sig, nil
}

// Client is used to prove ownership of an IP address, by answering a GPG
// challenge that the server sends at the address.
// A client must first register its Handler with an HTTPS server, before it can
// perform the challenge.
type Client struct {
	keyRing, keyId string
	signer         *openpgp.Entity
	challengeIP    string

	handler http.Handler
	// any error from one of the HTTP handle func is sent through errc, so
	// it can be communicated to the Challenge method, which can then error out
	// accordingly.
	errc chan error

	mu            sync.Mutex
	challengeDone bool
}

// NewClient returns a Client. keyRing and keyId are the GPG key ring and key ID
// used to fulfill the challenge. challengeIP is the address that client proves
// that it owns, by answering the challenge the server sends at this address.
func NewClient(keyRing, keyId, challengeIP string) (*Client, error) {
	signer, err := secretKeyEntity(keyRing, keyId)
	if err != nil {
		return nil, fmt.Errorf("could not get signer %v from keyRing %v: %v", keyId, keyRing, err)
	}
	cl := &Client{
		keyRing:     keyRing,
		keyId:       keyId,
		signer:      signer,
		challengeIP: challengeIP,
		errc:        make(chan error, 1),
	}
	handler := &clientHandler{
		cl: cl,
	}
	cl.handler = handler
	return cl, nil
}

// Handler returns the client's handler, that should be registered with an HTTPS
// server for the returned prefix, for the client to be able to receive the
// challenge.
func (cl *Client) Handler() (prefix string, h http.Handler) {
	return clientHandlerPrefix, cl.handler
}

// clientHandler is the "server" part of the Client, so it can receive and
// answer the Server's challenge.
type clientHandler struct {
	cl *Client
}

func (h *clientHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.cl.mu.Lock()
	defer h.cl.mu.Unlock()
	if r.URL.Path == clientHandlerPrefix+clientEndPointReady {
		h.handleReady(w, r)
		return
	}
	if r.URL.Path == clientHandlerPrefix+clientEndPointChallenge {
		h.handleChallenge(w, r)
		return
	}
	if r.URL.Path == clientHandlerPrefix+clientEndPointAck {
		h.handleACK(w, r)
		return
	}
	http.Error(w, "wrong path", 404)
}

// Challenge requests a challenge from the server running at serverAddr, which
// should be a host name or of the hostname:port form, and then fulfills that challenge.
func (cl *Client) Challenge(serverAddr string) error {
	if err := cl.listenSelfCheck(serverAddr); err != nil {
		return err
	}
	return cl.challenge(serverAddr)
}

// listenSelfCheck tests whether the client is ready to receive a challenge,
// i.e. that the caller has registered the client's handler with a server.
func (cl *Client) listenSelfCheck(serverAddr string) error {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         cl.challengeIP + SNISuffix,
		},
	}
	httpClient := &http.Client{
		Transport: tr,
	}
	errc := make(chan error, 1)
	respc := make(chan *http.Response, 1)
	var err error
	var resp *http.Response
	go func() {
		resp, err := httpClient.PostForm(fmt.Sprintf("https://localhost:%d%s%s", ClientChallengedPort, clientHandlerPrefix, clientEndPointReady),
			url.Values{"server": []string{serverAddr}})
		errc <- err
		respc <- resp
	}()
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	select {
	case err = <-errc:
		resp = <-respc
	case <-timeout.C:
		return errors.New("The client needs an HTTPS listener for its handler to answer the server's challenge. You need to call Handler and register the http.Handler with an HTTPS server, before calling Challenge.")
	}
	if err != nil {
		return fmt.Errorf("error starting challenge: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("error starting challenge: %v", resp.Status)
	}
	return nil
}

func (cl *Client) challenge(serverAddr string) error {
	token, err := cl.getToken(serverAddr)
	if err != nil {
		return fmt.Errorf("could not get token from server: %v", err)
	}

	errc := make(chan error, 1)
	go func() {
		if err := cl.sendClaim(serverAddr, token); err != nil {
			errc <- fmt.Errorf("error sending challenge claim to server: %v", err)
		}
	}()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	select {
	case err := <-errc:
		return err
	case err := <-cl.errc:
		return err // nil here on success.
	case <-timeout.C:
		return errors.New("challenge timeout")
	}
}

func (h *clientHandler) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "not a POST", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *clientHandler) handleChallenge(w http.ResponseWriter, r *http.Request) {
	cl := h.cl
	var stickyErr error
	defer func() {
		if stickyErr != nil {
			cl.errc <- stickyErr
		}
	}()
	if cl.challengeDone {
		stickyErr = errors.New("challenge already answered")
		http.Error(w, stickyErr.Error(), 500)
		return
	}
	if r.Method != "POST" {
		stickyErr = errors.New("not a POST")
		http.Error(w, stickyErr.Error(), http.StatusMethodNotAllowed)
		return
	}
	nonce, err := ioutil.ReadAll(r.Body)
	if err != nil {
		stickyErr = err
		http.Error(w, err.Error(), 500)
		return
	}
	var buf bytes.Buffer
	if err := openpgp.ArmoredDetachSign(
		&buf,
		cl.signer,
		bytes.NewReader(nonce),
		nil,
	); err != nil {
		stickyErr = err
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err := io.Copy(w, &buf); err != nil {
		stickyErr = fmt.Errorf("could not reply to challenge: %v", err)
		return
	}
	cl.challengeDone = true
}

func (h *clientHandler) handleACK(w http.ResponseWriter, r *http.Request) {
	cl := h.cl
	var stickyErr error
	defer func() {
		cl.errc <- stickyErr
	}()
	if r.Method != "POST" {
		stickyErr = errors.New("not a POST")
		http.Error(w, stickyErr.Error(), http.StatusMethodNotAllowed)
		return
	}
	if !cl.challengeDone {
		stickyErr = errors.New("ACK received before challenge was over")
		http.Error(w, stickyErr.Error(), http.StatusBadRequest)
		return
	}
	ack, err := ioutil.ReadAll(r.Body)
	if err != nil {
		stickyErr = err
		http.Error(w, err.Error(), 500)
		return
	}
	if string(ack) != "ACK" {
		stickyErr = fmt.Errorf("unexpected ACK message from server: %q", string(ack))
		http.Error(w, stickyErr.Error(), http.StatusBadRequest)
		return
	}
	// reset it for reuse of the client.
	cl.challengeDone = false
	if _, err := io.Copy(w, strings.NewReader("OK")); err != nil {
		log.Printf("non-critical error: could not reply to ACK: %v", err)
		return
	}
}

func (cl *Client) getToken(serverAddr string) (string, error) {
	resp, err := http.Get(fmt.Sprintf("https://%s/%s", serverAddr, serverEndPointToken))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	token := string(data)
	if len(token) == 0 {
		return "", errors.New("error getting initial token from server")
	}
	return token, nil
}

func (cl *Client) signToken(token string) (string, error) {
	var buf bytes.Buffer
	if err := openpgp.ArmoredDetachSign(
		&buf,
		cl.signer,
		strings.NewReader(token),
		nil,
	); err != nil {
		return "", err
	}
	return buf.String(), nil

}

func (cl Client) sendClaim(server, token string) error {
	pubkey, err := armorPubKey(cl.keyRing, cl.keyId)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := openpgp.ArmoredDetachSign(
		&buf,
		cl.signer,
		strings.NewReader(token),
		nil,
	); err != nil {
		return fmt.Errorf("could not sign token: %v", err)
	}
	values := url.Values{
		"pubkey":      {string(pubkey)},
		"challengeIP": {cl.challengeIP},
		"token":       {token},
		"signature":   {buf.String()},
	}
	resp, err := http.PostForm(fmt.Sprintf("https://%s/%s", server, serverEndPointChallenge), values)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		msg, err := ioutil.ReadAll(resp.Body)
		if err == nil {
			return fmt.Errorf("unexpected claim response: %v, %v", resp.Status, string(msg))
		}
		return fmt.Errorf("unexpected claim response: %v", resp.Status)
	}
	return nil
}

func armorPubKey(keyRing string, keyId string) ([]byte, error) {
	pubkey, err := publicKeyEntity(keyRing, keyId)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	wc, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, err
	}
	if err := pubkey.Serialize(wc); err != nil {
		return nil, err
	}
	if err := wc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func publicKeyEntity(keyRing string, keyId string) (*openpgp.Entity, error) {
	f, err := wkfs.Open(keyRing)
	if err != nil {
		return nil, fmt.Errorf("could not open keyRing %v: %v", keyRing, err)
	}
	defer f.Close()
	el, err := openpgp.ReadKeyRing(f)
	if err != nil {
		return nil, err
	}
	for _, e := range el {
		pubk := e.PrimaryKey
		if pubk.KeyIdShortString() == keyId {
			return e, nil
		}
	}
	return nil, fmt.Errorf("keyId %v not found in %v", keyId, keyRing)
}

func secretKeyEntity(keyRing string, keyId string) (*openpgp.Entity, error) {
	f, err := wkfs.Open(keyRing)
	if err != nil {
		return nil, fmt.Errorf("could not open keyRing %v: %v", keyRing, err)
	}
	defer f.Close()
	el, err := openpgp.ReadKeyRing(f)
	if err != nil {
		return nil, err
	}
	for _, e := range el {
		pubk := &e.PrivateKey.PublicKey
		// TODO(mpl): decrypt private key if it is passphrase-encrypted
		if pubk.KeyIdShortString() == keyId {
			return e, nil
		}
	}
	return nil, fmt.Errorf("keyId %v not found in %v", keyId, keyRing)
}
