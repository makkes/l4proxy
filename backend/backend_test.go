package backend_test

import (
	"context"
	"log"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/stdr"
	"github.com/stretchr/testify/require"

	"github.com/makkes/l4proxy/backend"
)

func TestNewBackend(t *testing.T) {
	t.Parallel()

	network := "tcp4"
	addr := "1.2.3.4:5544"
	b := backend.NewBackend(network, addr, logr.Discard())
	require.Equal(t, addr, b.Addr)
	require.Equal(t, network, b.Network)
}

func TestStartFailsWithZeroHealthInterval(t *testing.T) {
	t.Parallel()

	b := backend.NewBackend("tcp4", "1.2.3.4:4912", logr.Discard())
	err := b.Start(0)
	require.Errorf(t, err, "foobar")
}

func TestStartSucceedsWithExpectedHealthInterval(t *testing.T) {
	t.Parallel()

	b := backend.NewBackend("tcp4", "1.2.3.4:4912", logr.Discard())
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
	f := func(_ logr.Logger, to net.Conn, from net.Conn, _ <-chan struct{}, _ chan<- struct{}) <-chan struct{} {
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

	b := backend.NewBackend(backendSrvListener.Addr().Network(), backendSrvListener.Addr().String(), logr.Discard(), backend.WithProxyFunc(f))

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

	go func() {
		conn, err := be.Accept()
		require.NoError(t, err, "could not accept connection")
		buf := make([]byte, 5)

		n, err := conn.Read(buf)
		require.NoError(t, err, "could not read from backend conn")
		require.Equal(t, 5, n, "unexpected number of bytes received from client")
		require.Equal(t, []byte("hello"), buf)

		n, err = conn.Write([]byte("hello yourself"))
		require.NoError(t, err, "could not write to client")
		require.Equal(t, 14, n, "unexpected number of bytes written to client")

		require.NoError(t, conn.Close(), "could not close backend conn")
	}()

	logger := stdr.New(log.New(os.Stderr, "", log.Lmicroseconds))
	b := backend.NewBackend("tcp4", be.Addr().String(), logger)
	go func() {
		n, err := clientIn.Write([]byte("hello"))
		require.NoError(t, err, "could not write to client conn")
		require.Equal(t, 5, n, "unexpected number of bytes written to backend")
	}()

	go func() {
		buf := make([]byte, 14)
		n, err := clientIn.Read(buf)
		require.NoError(t, err, "could not read from client conn")
		require.Equal(t, 14, n, "unexpected number of bytes received from backend")

		require.NoError(t, clientOut.Close(), "could not close client conn")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	keepaliveChan := make(chan struct{}, 2)
	require.NoError(t, b.HandleConn(ctx, clientOut, keepaliveChan))
}

func TestUDPConnectionHandling(t *testing.T) {
	t.Parallel()

	clientIn, clientOut := net.Pipe()

	be, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP:   net.ParseIP("localhost"),
		Port: 0,
	})
	require.NoError(t, err, "could not start backend listener")

	go func() {
		buf := make([]byte, 5)

		n, addr, err := be.ReadFromUDP(buf)
		require.NoError(t, err, "could not read from backend conn")
		require.Equal(t, 5, n, "unexpected number of bytes received from client")
		require.Equal(t, []byte("hello"), buf)

		n, err = be.WriteToUDP([]byte("hello yourself"), addr)
		require.NoError(t, err, "could not write to client")
		require.Equal(t, 14, n, "unexpeted number of bytes written to client")

		require.NoError(t, be.Close(), "could not close backend conn")
	}()

	b := backend.NewBackend("udp4", be.LocalAddr().String(), stdr.New(nil))

	go func() {
		n, err := clientIn.Write([]byte("hello"))
		require.NoError(t, err, "could not write to client conn")
		require.Equal(t, 5, n, "unexpected number of bytes written to backend")
	}()

	go func() {
		buf := make([]byte, 14)
		n, err := clientIn.Read(buf)
		require.NoError(t, err, "could not read from client conn")
		require.Equal(t, 14, n, "unexpected number of bytes received from backend")

		require.NoError(t, clientOut.Close(), "could not close client conn")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	keepaliveChan := make(chan struct{}, 2)
	require.NoError(t, b.HandleConn(ctx, clientOut, keepaliveChan))
}
