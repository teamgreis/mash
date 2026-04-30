package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/mash-protocol/mash-go/pkg/log"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// ServerConfig configures a MASH server.
type ServerConfig struct {
	// TLSConfig contains TLS settings.
	TLSConfig *TLSConfig

	// Address to listen on (e.g., ":8443" or "127.0.0.1:8443").
	Address string

	// RequireClientCert requires clients to present a valid certificate.
	RequireClientCert bool

	// MaxMessageSize is the maximum message size (default: 8 KB per DEC-069).
	MaxMessageSize uint32

	// Logger for protocol logging (optional).
	Logger log.Logger

	// OnConnect is called when a new connection is established.
	OnConnect func(conn *ServerConn)

	// OnDisconnect is called when a connection is closed.
	OnDisconnect func(conn *ServerConn)

	// OnMessage is called when a message is received.
	OnMessage func(conn *ServerConn, msg []byte)

	// OnError is called when an error occurs.
	OnError func(conn *ServerConn, err error)
}

// Server is a MASH TLS server that accepts connections from controllers.
type Server struct {
	config   ServerConfig
	tlsConf  *tls.Config
	listener net.Listener

	// Active connections
	conns   map[*ServerConn]struct{}
	connsMu sync.RWMutex

	// State
	running atomic.Bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewServer creates a new MASH server.
func NewServer(config ServerConfig) (*Server, error) {
	if config.TLSConfig == nil {
		return nil, fmt.Errorf("TLSConfig is required")
	}
	if config.Address == "" {
		config.Address = fmt.Sprintf(":%d", DefaultPort)
	}
	if config.MaxMessageSize == 0 {
		config.MaxMessageSize = DefaultMaxMessageSize
	}

	// Build TLS config
	tlsConf, err := NewServerTLSConfig(config.TLSConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS config: %w", err)
	}

	// Adjust client auth based on config
	if config.RequireClientCert {
		tlsConf.ClientAuth = tls.RequireAndVerifyClientCert
	} else if config.TLSConfig.InsecureSkipVerify {
		tlsConf.ClientAuth = tls.NoClientCert
	}

	return &Server{
		config:  config,
		tlsConf: tlsConf,
		conns:   make(map[*ServerConn]struct{}),
	}, nil
}

// Start starts the server and begins accepting connections.
func (s *Server) Start(ctx context.Context) error {
	if s.running.Load() {
		return fmt.Errorf("server already running")
	}

	s.ctx, s.cancel = context.WithCancel(ctx)

	// Create listener
	listener, err := net.Listen("tcp", s.config.Address)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	s.listener = listener

	s.running.Store(true)

	// Start accept loop
	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

// Stop stops the server and closes all connections.
func (s *Server) Stop() error {
	if !s.running.Load() {
		return nil
	}

	s.running.Store(false)
	s.cancel()

	// Close listener to stop accept loop
	if s.listener != nil {
		s.listener.Close()
	}

	// Close all connections
	s.connsMu.Lock()
	for conn := range s.conns {
		conn.Close()
	}
	s.connsMu.Unlock()

	// Wait for goroutines
	s.wg.Wait()

	return nil
}

// Addr returns the server's listen address.
func (s *Server) Addr() net.Addr {
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}

// ConnectionCount returns the number of active connections.
func (s *Server) ConnectionCount() int {
	s.connsMu.RLock()
	defer s.connsMu.RUnlock()
	return len(s.conns)
}

// acceptLoop accepts incoming connections.
func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for s.running.Load() {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.running.Load() {
				// Real error
				if s.config.OnError != nil {
					s.config.OnError(nil, fmt.Errorf("accept error: %w", err))
				}
			}
			continue
		}

		// Handle connection in goroutine
		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

// handleConnection processes a single connection.
func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()

	// TLS handshake
	tlsConn := tls.Server(conn, s.tlsConf)
	if err := tlsConn.HandshakeContext(s.ctx); err != nil {
		conn.Close()
		if s.config.OnError != nil {
			s.config.OnError(nil, fmt.Errorf("TLS handshake failed: %w", err))
		}
		return
	}

	// Verify TLS version and ALPN
	state := tlsConn.ConnectionState()
	if err := VerifyConnection(state); err != nil {
		tlsConn.Close()
		if s.config.OnError != nil {
			s.config.OnError(nil, err)
		}
		return
	}

	// Verify client certificate if required
	if s.config.RequireClientCert && len(state.PeerCertificates) == 0 {
		tlsConn.Close()
		if s.config.OnError != nil {
			s.config.OnError(nil, fmt.Errorf("client certificate required but not provided"))
		}
		return
	}

	// Generate unique connection ID
	connID := uuid.New().String()

	// Create server connection wrapper
	framer := NewFramerWithMaxSize(tlsConn, s.config.MaxMessageSize)
	if s.config.Logger != nil {
		framer.SetLogger(s.config.Logger, connID)
	}

	sconn := &ServerConn{
		conn:       tlsConn,
		framer:     framer,
		tlsState:   state,
		server:     s,
		closeCh:    make(chan struct{}),
		remoteAddr: conn.RemoteAddr(),
		connID:     connID,
	}

	// Log connected state
	if s.config.Logger != nil {
		s.config.Logger.Log(log.Event{
			Timestamp:    time.Now(),
			ConnectionID: connID,
			Layer:        log.LayerTransport,
			Category:     log.CategoryState,
			RemoteAddr:   conn.RemoteAddr().String(),
			StateChange: &log.StateChangeEvent{
				Entity:   log.StateEntityConnection,
				NewState: "CONNECTED",
			},
		})
	}

	// Register connection
	s.connsMu.Lock()
	s.conns[sconn] = struct{}{}
	s.connsMu.Unlock()

	// Notify connect
	if s.config.OnConnect != nil {
		s.config.OnConnect(sconn)
	}

	// Read loop
	sconn.readLoop()

	// Unregister connection
	s.connsMu.Lock()
	delete(s.conns, sconn)
	s.connsMu.Unlock()

	// Log disconnected state
	if s.config.Logger != nil {
		s.config.Logger.Log(log.Event{
			Timestamp:    time.Now(),
			ConnectionID: sconn.connID,
			Layer:        log.LayerTransport,
			Category:     log.CategoryState,
			RemoteAddr:   conn.RemoteAddr().String(),
			StateChange: &log.StateChangeEvent{
				Entity:   log.StateEntityConnection,
				OldState: "CONNECTED",
				NewState: "DISCONNECTED",
			},
		})
	}

	// Notify disconnect
	if s.config.OnDisconnect != nil {
		s.config.OnDisconnect(sconn)
	}
}

// ServerConn represents a client connection to the server.
type ServerConn struct {
	conn       *tls.Conn
	framer     *Framer
	tlsState   tls.ConnectionState
	server     *Server
	closeCh    chan struct{}
	closeOnce  sync.Once
	remoteAddr net.Addr
	connID     string // Unique connection identifier

	// Synchronization
	writeMu sync.Mutex
}

// RemoteAddr returns the remote address of the client.
func (c *ServerConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

// ConnID returns the unique connection identifier.
func (c *ServerConn) ConnID() string {
	return c.connID
}

// TLSState returns the TLS connection state.
func (c *ServerConn) TLSState() tls.ConnectionState {
	return c.tlsState
}

// Send sends a message to the client.
func (c *ServerConn) Send(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.framer.WriteFrame(data)
}

// Close closes the connection.
func (c *ServerConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closeCh)
		err = c.conn.Close()
	})
	return err
}

// readLoop reads messages from the connection.
func (c *ServerConn) readLoop() {
	for {
		select {
		case <-c.closeCh:
			return
		case <-c.server.ctx.Done():
			return
		default:
		}

		data, err := c.framer.ReadFrame()
		if err != nil {
			// Connection closed or error
			if c.server.config.OnError != nil && c.server.running.Load() {
				select {
				case <-c.closeCh:
					// Already closing, don't report
				default:
					c.server.config.OnError(c, err)
				}
			}
			return
		}

		// Use PeekMessageType to properly distinguish message types.
		// This is important because control messages and requests share
		// similar CBOR structure (integer keys 1 and 2), so naive decoding
		// could misinterpret a request as a control message.
		msgType, peekErr := wire.PeekMessageType(data)
		if peekErr == nil && msgType == wire.MessageTypeControl {
			if ctrlMsg, err := wire.DecodeControlMessage(data); err == nil {
				c.handleControlMessage(ctrlMsg)
				continue
			}
		}

		// Regular message - pass to handler
		if c.server.config.OnMessage != nil {
			c.server.config.OnMessage(c, data)
		}
	}
}

// handleControlMessage processes control messages.
func (c *ServerConn) handleControlMessage(msg *wire.ControlMessage) {
	// Log incoming control message
	c.logControlMessage(msg.Type, log.DirectionIn)

	switch msg.Type {
	case wire.ControlPing:
		// Respond with pong
		pongMsg, _ := EncodePong(msg.Sequence)
		c.Send(pongMsg)
		// Log outgoing pong
		c.logControlMessage(wire.ControlPong, log.DirectionOut)

	case wire.ControlPong:
		// Ignore pongs on server side (client initiated keep-alive)

	case wire.ControlClose:
		// Log outgoing close acknowledgment
		c.logControlMessage(wire.ControlClose, log.DirectionOut)
		// Peer initiated close - acknowledge and close
		closeMsg, _ := EncodeClose()
		c.Send(closeMsg)
		c.Close()
	}
}

// logControlMessage logs a control message event.
func (c *ServerConn) logControlMessage(msgType wire.ControlMessageType, direction log.Direction) {
	if c.server.config.Logger == nil {
		return
	}

	var ctrlMsgType log.ControlMsgType
	switch msgType {
	case wire.ControlPing:
		ctrlMsgType = log.ControlMsgPing
	case wire.ControlPong:
		ctrlMsgType = log.ControlMsgPong
	case wire.ControlClose:
		ctrlMsgType = log.ControlMsgClose
	default:
		return // Unknown control message type
	}

	c.server.config.Logger.Log(log.Event{
		Timestamp:    time.Now(),
		ConnectionID: c.connID,
		Direction:    direction,
		Layer:        log.LayerTransport,
		Category:     log.CategoryControl,
		RemoteAddr:   c.remoteAddr.String(),
		ControlMsg: &log.ControlMsgEvent{
			Type: ctrlMsgType,
		},
	})
}

// Control message encoding helpers

// ControlPing is the ping control message type.
const ControlPing = wire.ControlPing

// ControlPong is the pong control message type.
const ControlPong = wire.ControlPong

// ControlClose is the close control message type.
const ControlClose = wire.ControlClose

// EncodePing encodes a ping control message.
func EncodePing(seq uint32) ([]byte, error) {
	return wire.EncodeControlMessage(&wire.ControlMessage{
		Type:     wire.ControlPing,
		Sequence: seq,
	})
}

// EncodePong encodes a pong control message.
func EncodePong(seq uint32) ([]byte, error) {
	return wire.EncodeControlMessage(&wire.ControlMessage{
		Type:     wire.ControlPong,
		Sequence: seq,
	})
}

// EncodeClose encodes a close control message.
func EncodeClose() ([]byte, error) {
	return wire.EncodeControlMessage(&wire.ControlMessage{
		Type: wire.ControlClose,
	})
}

// DecodeControlMessage decodes a control message and returns its type and sequence.
func DecodeControlMessage(data []byte) (wire.ControlMessageType, uint32, error) {
	msg, err := wire.DecodeControlMessage(data)
	if err != nil {
		return 0, 0, err
	}
	return msg.Type, msg.Sequence, nil
}
