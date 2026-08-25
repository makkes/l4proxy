package backend_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/makkes/l4proxy/backend"
)

func TestNewBackend(t *testing.T) {
	t.Parallel()

	network := "tcp4"
	addr := "1.2.3.4:5544"
	b := backend.NewBackend(network, addr, slog.New(slog.DiscardHandler))
	require.Equal(t, addr, b.Addr)
	require.Equal(t, network, b.Network)
}

func TestStartFailsWithZeroHealthInterval(t *testing.T) {
	t.Parallel()

	b := backend.NewBackend("tcp4", "1.2.3.4:4912", slog.New(slog.DiscardHandler))
	err := b.Start(0)
	require.Errorf(t, err, "foobar")
}

func TestStartSucceedsWithExpectedHealthInterval(t *testing.T) {
	t.Parallel()

	b := backend.NewBackend("tcp4", "1.2.3.4:4912", slog.New(slog.DiscardHandler))
	err := b.Start(42)
	require.NoError(t, err)
}

func TestNewBackendWithCustomProxy(t *testing.T) {
	t.Parallel()

	backendSrvAddr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	require.NoError(t, err, "resolving test address should succeed")

	backendSrvListener, err := net.ListenTCP("tcp", backendSrvAddr)
	require.NoError(t, err, "starting test listener should succeed")
	defer func() {
		require.NoError(t, backendSrvListener.Close(), "closing test listener should succeed")
	}()

	pConn, _ := net.Pipe()
	var calls atomic.Int32
	f := func(_ *slog.Logger, to net.Conn, from net.Conn, _ <-chan struct{}, _ chan<- struct{}) <-chan struct{} {
		cnt := calls.Add(1)
		// first, the connection from client to backend should be proxied
		if cnt == 1 {
			require.Equal(t, pConn.RemoteAddr(), from.RemoteAddr())
			require.Equal(t, backendSrvListener.Addr(), to.RemoteAddr())
		}

		// next, the connection from backend back to the client should be proxied
		if cnt == 2 {
			require.Equal(t, backendSrvListener.Addr(), from.RemoteAddr())
			require.Equal(t, pConn.RemoteAddr(), to.RemoteAddr())
		}

		res := make(chan struct{})
		close(res)
		return res
	}

	b := backend.NewBackend(
		backendSrvListener.Addr().Network(),
		backendSrvListener.Addr().String(),
		slog.New(slog.DiscardHandler),
		backend.WithProxyFunc(f),
	)

	require.NoError(t, b.HandleConn(t.Context(), pConn, nil), "handling connection should succeed")
	require.NoError(t, pConn.Close(), "closing pipe should succeed")
	require.Equal(t, int32(2), calls.Load(), "proxy should be called twice, for the client=>backend and for the backend=>client connection")
}

func TestTCPConnectionHandling(t *testing.T) {
	t.Parallel()

	clientIn, clientOut := net.Pipe()
	be, err := net.ListenTCP("tcp4", &net.TCPAddr{
		IP:   net.ParseIP("localhost"),
		Port: 0,
	})
	require.NoError(t, err, "could not start backend listener")

	backendErrCh := make(chan error, 1)
	go func() {
		backendErrCh <- serveTCPTestConnection(be)
	}()

	b := backend.NewBackend("tcp4", be.Addr().String(), slog.New(slog.DiscardHandler))
	clientWriteErrCh := make(chan error, 1)
	go func() {
		clientWriteErrCh <- writeTestRequest(clientIn)
	}()

	clientReadErrCh := make(chan error, 1)
	go func() {
		clientReadErrCh <- readTestResponse(clientIn, clientOut)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	keepaliveChan := make(chan struct{}, 2)
	require.NoError(t, b.HandleConn(ctx, clientOut, keepaliveChan))
	backendErr := <-backendErrCh
	clientWriteErr := <-clientWriteErrCh
	clientReadErr := <-clientReadErrCh
	require.NoError(t, backendErr)
	require.NoError(t, clientWriteErr)
	require.NoError(t, clientReadErr)
	require.NoError(t, be.Close(), "could not close backend listener")
}

func TestUDPConnectionHandling(t *testing.T) {
	t.Parallel()

	clientIn, clientOut := net.Pipe()

	be, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP:   net.ParseIP("localhost"),
		Port: 0,
	})
	require.NoError(t, err, "could not start backend listener")

	backendErrCh := make(chan error, 1)
	go func() {
		backendErrCh <- serveUDPTestConnection(be)
	}()

	b := backend.NewBackend("udp4", be.LocalAddr().String(), slog.New(slog.DiscardHandler))

	clientWriteErrCh := make(chan error, 1)
	go func() {
		clientWriteErrCh <- writeTestRequest(clientIn)
	}()

	clientReadErrCh := make(chan error, 1)
	go func() {
		clientReadErrCh <- readTestResponse(clientIn, clientOut)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	keepaliveChan := make(chan struct{}, 2)
	require.NoError(t, b.HandleConn(ctx, clientOut, keepaliveChan))
	backendErr := <-backendErrCh
	clientWriteErr := <-clientWriteErrCh
	clientReadErr := <-clientReadErrCh
	require.NoError(t, backendErr)
	require.NoError(t, clientWriteErr)
	require.NoError(t, clientReadErr)
}

const (
	testRequest  = "hello"
	testResponse = "hello yourself"
)

func serveTCPTestConnection(listener *net.TCPListener) (resultErr error) {
	conn, err := listener.Accept()
	if err != nil {
		return fmt.Errorf("could not accept connection: %w", err)
	}
	defer func() {
		if err := conn.Close(); resultErr == nil && err != nil {
			resultErr = fmt.Errorf("could not close backend conn: %w", err)
		}
	}()

	if err := readExpectedMessage(conn, testRequest); err != nil {
		return err
	}
	return writeExpectedMessage(conn, testResponse)
}

func serveUDPTestConnection(conn *net.UDPConn) (resultErr error) {
	defer func() {
		if err := conn.Close(); resultErr == nil && err != nil {
			resultErr = fmt.Errorf("could not close backend conn: %w", err)
		}
	}()

	buf := make([]byte, len(testRequest))
	n, addr, err := conn.ReadFromUDP(buf)
	if err != nil {
		return fmt.Errorf("could not read from backend conn: %w", err)
	}
	if err := validateMessage(buf, n, testRequest); err != nil {
		return err
	}

	n, err = conn.WriteToUDP([]byte(testResponse), addr)
	if err != nil {
		return fmt.Errorf("could not write to client: %w", err)
	}
	if n != len(testResponse) {
		return fmt.Errorf("unexpected number of bytes written to client: got %d, want %d", n, len(testResponse))
	}

	return nil
}

func writeTestRequest(conn net.Conn) error {
	return writeExpectedMessage(conn, testRequest)
}

func writeExpectedMessage(conn net.Conn, message string) error {
	n, err := conn.Write([]byte(message))
	if err != nil {
		return fmt.Errorf("could not write message: %w", err)
	}
	if n != len(message) {
		return fmt.Errorf("unexpected number of bytes written: got %d, want %d", n, len(message))
	}

	return nil
}

func readTestResponse(reader, closer net.Conn) error {
	if err := readExpectedMessage(reader, testResponse); err != nil {
		return err
	}
	if err := closer.Close(); err != nil {
		return fmt.Errorf("could not close client conn: %w", err)
	}

	return nil
}

func readExpectedMessage(conn net.Conn, expected string) error {
	buf := make([]byte, len(expected))
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("could not read message: %w", err)
	}

	return validateMessage(buf, n, expected)
}

func validateMessage(buf []byte, n int, expected string) error {
	if n != len(expected) {
		return fmt.Errorf("unexpected number of bytes received: got %d, want %d", n, len(expected))
	}
	if !bytes.Equal([]byte(expected), buf) {
		return fmt.Errorf("unexpected bytes received: got %q, want %q", buf, expected)
	}

	return nil
}
