package listen

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// NewFlag returns a flag that implements the flag.Value interface.
func NewFlag(flagName, defaultValue string, serverType string) *Addr {
	addr := &Addr{
		s: defaultValue,
	}
	flag.Var(addr, flagName, Usage(serverType))
	return addr
}

// Listen is a replacement for net.Listen that also respects runsit
// listeners: port, :port, ip:port, FD:<fd_num>, ADDR:<name> or <name>
// named ports.
// Listeners are always TCP.
func Listen(addr string) (net.Listener, error) {
	a := &Addr{s: addr}
	return a.Listen()
}

// Usage returns a descriptive usage message for a flag given the name
// of thing being addressed.
func Usage(name string) string {
	if name == "" {
		name = "Listen address"
	}
	if !strings.HasSuffix(name, " address") {
		name += " address"
	}
	return name + "; may be port, :port, ip:port, FD:<fd_num>, or ADDR:<name> to use named runsit ports"
}

// Addr is a flag variable.  Use like:
//
// var webPort listen.Addr
// flag.Var(&webPort, "web_addr", listen.Usage("Web server address"))
// flag.Parse()
// webListener, err := webPort.Listen()
type Addr struct {
	s   string
	ln  net.Listener
	err error
}

func (a *Addr) String() string {
	return a.s
}

// Set implements the flag.Value interface.
func (a *Addr) Set(v string) error {
	a.s = v

	// Try the requested port by runsit port name first.
	fd, ok, err := namedPort(v)
	if err != nil {
		return err
	}
	if ok {
		return a.listenOnFD(fd)
	}

	if strings.HasPrefix(v, "FD:") {
		fdStr := v[len("FD:"):]
		fd, err := strconv.ParseUint(fdStr, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid file descriptor %q: %v", fdStr, err)
		}
		return a.listenOnFD(uintptr(fd))
	}

	ipPort := v
	if isPort(v) {
		ipPort = ":" + v
	}

	_, _, err = net.SplitHostPort(ipPort)
	if err != nil {
		return fmt.Errorf("invalid PORT or IP:PORT %q: %v", v, err)
	}
	a.ln, err = net.Listen("tcp", ipPort)
	return err
}

func isPort(s string) bool {
	_, err := strconv.ParseUint(s, 10, 16)
	return err == nil
}

func (a *Addr) listenOnFD(fd uintptr) (err error) {
	f := os.NewFile(fd, fmt.Sprintf("fd #%d from runsit parent", fd))
	a.ln, err = net.FileListener(f)
	return
}

func namedPort(name string) (fd uintptr, ok bool, err error) {
	s := os.Getenv("RUNSIT_PORTFD_" + name)
	if s == "" {
		return
	}
	u64, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return
	}
	return uintptr(u64), true, nil
}

var _ flag.Value = (*Addr)(nil)

// Listen returns the address's TCP listener.
func (a *Addr) Listen() (net.Listener, error) {
	// Start the listener now, if there's a default
	// and nothing's called Set yet.
	if a.err == nil && a.ln == nil && a.s != "" {
		if err := a.Set(a.s); err != nil {
			return nil, err
		}
	}
	if a.err != nil {
		return nil, a.err
	}
	if a.ln != nil {
		return a.ln, nil
	}
	return nil, errors.New("listen: no error or listener")
}
