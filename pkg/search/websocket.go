/*
Copyright 2013 The Camlistore Authors

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

package search

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"camlistore.org/third_party/github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 10 << 10
)

type wsConn struct {
	ws   *websocket.Conn
	send chan []byte // Buffered channel of outbound messages.
	sh   *Handler
}

type wsHub struct {
	sh         *Handler
	register   chan *wsConn
	unregister chan *wsConn
	conns      map[*wsConn]bool
}

func newWebsocketHub(sh *Handler) *wsHub {
	return &wsHub{
		sh:         sh,
		register:   make(chan *wsConn),
		unregister: make(chan *wsConn),
		conns:      make(map[*wsConn]bool),
	}
}

func (h *wsHub) run() {
	for {
		select {
		case c := <-h.register:
			h.conns[c] = true
		case c := <-h.unregister:
			delete(h.conns, c)
			close(c.send)
		}
	}
}

// readPump pumps messages from the websocket connection to the hub.
func (c *wsConn) readPump() {
	defer func() {
		c.sh.wsHub.unregister <- c
		c.ws.Close()
	}()
	c.ws.SetReadLimit(maxMessageSize)
	c.ws.SetReadDeadline(time.Now().Add(pongWait))
	c.ws.SetPongHandler(func(string) error { c.ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.ws.ReadMessage()
		if err != nil {
			break
		}
		log.Printf("Got websocket message %q", message)
		c.send <- []byte(fmt.Sprintf(`{"msg": "Server says hi to Javascript. Time is %v"}`, time.Now()))
	}
}

// write writes a message with the given message type and payload.
func (c *wsConn) write(mt int, payload []byte) error {
	c.ws.SetWriteDeadline(time.Now().Add(writeWait))
	return c.ws.WriteMessage(mt, payload)
}

// writePump pumps messages from the hub to the websocket connection.
func (c *wsConn) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.ws.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				c.write(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.write(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.write(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

func (sh *Handler) serveWebSocket(rw http.ResponseWriter, req *http.Request) {
	ws, err := websocket.Upgrade(rw, req, nil, 1024, 1024)
	if _, ok := err.(websocket.HandshakeError); ok {
		http.Error(rw, "Not a websocket handshake", 400)
		return
	} else if err != nil {
		log.Println(err)
		return
	}
	c := &wsConn{
		ws:   ws,
		send: make(chan []byte, 256),
		sh:   sh,
	}
	sh.wsHub.register <- c
	go c.writePump()
	c.readPump()
}
