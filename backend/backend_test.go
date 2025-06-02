package backend

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/stdr"
	"github.com/stretchr/testify/require"
)

func TestNewBackend(t *testing.T) {
	network := "tcp4"
	addr := "1.2.3.4:5544"
	b := NewBackend(network, addr, logr.Discard())
	require.Equal(t, addr, b.Addr)
	require.Equal(t, network, b.Network)
}

func TestStartFailsWithZeroHealthInterval(t *testing.T) {
	b := NewBackend("tcp4", "1.2.3.4:4912", logr.Discard())
	err := b.Start(0)
	require.Errorf(t, err, "foobar")
}

func TestStartSucceedsWithExpectedHealthInterval(t *testing.T) {
	b := NewBackend("tcp4", "1.2.3.4:4912", logr.Discard())
	err := b.Start(42)
	require.Nil(t, err)
}

func TestNewBackendWithOptions(t *testing.T) {
	f := func(log logr.Logger, to net.Conn, from net.Conn, quitChan <-chan struct{}, keepaliveChan chan<- struct{}) <-chan struct{} {
		return nil
	}
	b := NewBackend("", "", logr.Discard(), WithProxyFunc(f))

	require.Equal(t, fmt.Sprintf("%p", f), fmt.Sprintf("%p", b.proxy))
}

func TestTCPConnectionHandling(t *testing.T) {
	stdr.SetVerbosity(99)
	goroutinesStart := runtime.NumGoroutine()
	clientIn, clientOut := net.Pipe()
	backend, err := net.ListenTCP("tcp4", &net.TCPAddr{
		IP:   net.ParseIP("localhost"),
		Port: 0,
	})
	require.NoError(t, err, "could not start backend listener")

	go func() {
		conn, err := backend.Accept()
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

	log := stdr.New(log.New(os.Stderr, "", log.Lmicroseconds))
	b := NewBackend("tcp4", backend.Addr().String(), log)
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
	require.Nil(t, b.HandleConn(ctx, clientOut, keepaliveChan))
	require.Equal(t, goroutinesStart, runtime.NumGoroutine(), "unexpected number of goroutines")
}

func TestUDPConnectionHandling(t *testing.T) {
	goroutinesStart := runtime.NumGoroutine()
	clientIn, clientOut := net.Pipe()

	backend, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP:   net.ParseIP("localhost"),
		Port: 0,
	})
	require.NoError(t, err, "could not start backend listener")

	go func() {
		buf := make([]byte, 5)

		n, addr, err := backend.ReadFromUDP(buf)
		require.NoError(t, err, "could not read from backend conn")
		require.Equal(t, 5, n, "unexpected number of bytes received from client")
		require.Equal(t, []byte("hello"), buf)

		n, err = backend.WriteToUDP([]byte("hello yourself"), addr)
		require.NoError(t, err, "could not write to client")
		require.Equal(t, 14, n, "unexpeted number of bytes written to client")

		require.NoError(t, backend.Close(), "could not close backend conn")
	}()

	b := NewBackend("udp4", backend.LocalAddr().String(), stdr.New(nil))

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
	require.Nil(t, b.HandleConn(ctx, clientOut, keepaliveChan))
	require.Equal(t, goroutinesStart, runtime.NumGoroutine(), "unexpected number of goroutines")
}
