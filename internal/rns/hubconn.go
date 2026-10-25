package rns

import (
	"errors"
	"net"
	"sync"
	"time"

	"git.quad4.io/Networks/Reticulum-Go/pkg/common"
	"git.quad4.io/Networks/Reticulum-Go/pkg/interfaces"
)

// hubConn is one TCP connection to a hub, framed with HDLC like every
// Reticulum TCP interface. It replaces interfaces.TCPClientInterface, whose
// reconnect loop writes its state without the lock (a data race under
// -race) and keeps retrying a dead hub on its own; here a dropped
// connection just goes offline and HubManager picks another hub.
type hubConn struct {
	interfaces.BaseInterface

	addr string
	wmu  sync.Mutex // serializes frames on conn
	conn net.Conn   // guarded by BaseInterface.Mutex
}

func newHubConn(addr string) *hubConn {
	return &hubConn{
		BaseInterface: interfaces.NewBaseInterface(addr, common.IFTypeTCP, true),
		addr:          addr,
	}
}

// Start connects in the background; the interface goes online when the
// TCP connection is up.
func (h *hubConn) Start() error {
	go func() {
		conn, err := net.DialTimeout("tcp", h.addr, 15*time.Second)
		if err != nil {
			return
		}
		h.Mutex.Lock()
		if h.Detached {
			h.Mutex.Unlock()
			conn.Close()
			return
		}
		h.conn, h.Online = conn, true
		h.Mutex.Unlock()
		h.readLoop(conn)
	}()
	return nil
}

// Stop closes the connection for good.
func (h *hubConn) Stop() error {
	h.Mutex.Lock()
	h.Detached, h.Online = true, false
	conn := h.conn
	h.conn = nil
	h.Mutex.Unlock()
	if conn != nil {
		conn.Close()
	}
	return nil
}

func (h *hubConn) connected() bool {
	h.Mutex.RLock()
	defer h.Mutex.RUnlock()
	return h.Online && h.conn != nil
}

func (h *hubConn) GetConn() net.Conn {
	h.Mutex.RLock()
	defer h.Mutex.RUnlock()
	return h.conn
}

func (h *hubConn) Send(data []byte, _ string) error {
	masked, err := common.ApplyIFACOutbound(h, data)
	if err != nil {
		return err
	}
	return h.ProcessOutgoing(masked)
}

func (h *hubConn) ProcessOutgoing(data []byte) error {
	conn := h.GetConn()
	if conn == nil || !h.connected() {
		return errors.New("hub offline")
	}
	frame := make([]byte, 0, len(data)+8)
	frame = append(frame, interfaces.HDLCFlag)
	for _, b := range data {
		if b == interfaces.HDLCFlag || b == interfaces.HDLCEsc {
			frame = append(frame, interfaces.HDLCEsc, b^interfaces.HDLCEscMask)
		} else {
			frame = append(frame, b)
		}
	}
	frame = append(frame, interfaces.HDLCFlag)
	h.wmu.Lock()
	_, err := conn.Write(frame)
	h.wmu.Unlock()
	if err != nil {
		h.offline(conn)
		return err
	}
	h.Mutex.Lock()
	h.TxBytes += uint64(len(frame))
	h.TxPackets++
	h.Mutex.Unlock()
	return nil
}

func (h *hubConn) offline(conn net.Conn) {
	h.Mutex.Lock()
	if h.conn == conn {
		h.Online = false
	}
	h.Mutex.Unlock()
	conn.Close()
}

func (h *hubConn) readLoop(conn net.Conn) {
	defer h.offline(conn)
	maxFrame := 2*h.GetMTU() + 32
	buf := make([]byte, 4096)
	frame := make([]byte, 0, maxFrame)
	inFrame, escape := false, false
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		for _, b := range buf[:n] {
			switch {
			case b == interfaces.HDLCFlag:
				if inFrame && len(frame) > 0 {
					h.ProcessIncoming(append([]byte(nil), frame...))
				}
				frame, inFrame, escape = frame[:0], true, false
			case !inFrame:
			case b == interfaces.HDLCEsc:
				escape = true
			default:
				if escape {
					b ^= interfaces.HDLCEscMask
					escape = false
				}
				if len(frame) >= maxFrame {
					frame, inFrame = frame[:0], false
					continue
				}
				frame = append(frame, b)
			}
		}
	}
}
