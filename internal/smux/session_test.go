package smux

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func init() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	ln, err := net.Listen("tcp", "127.0.0.1:19999")
	if err != nil {
		// handle error
		panic(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				// handle error
			}
			go handleConnection(conn)
		}
	}()
}

func handleConnection(conn net.Conn) {
	session, _ := Server(conn, nil)
	for {
		if stream, err := session.AcceptStream(); err == nil {
			go func(s io.ReadWriteCloser) {
				buf := make([]byte, 65536)
				for {
					n, err := s.Read(buf)
					if err != nil {
						return
					}
					s.Write(buf[:n])
				}
			}(stream)
		} else {
			return
		}
	}
}

func randUint32() uint32 {
	buf := make([]byte, 4)
	rand.Read(buf)
	return binary.LittleEndian.Uint32(buf)
}

func TestEcho(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	const N = 100
	buf := make([]byte, 10)
	var sent string
	var received string
	for i := 0; i < N; i++ {
		msg := fmt.Sprintf("hello%v", i)
		stream.Write([]byte(msg))
		sent += msg
		if n, err := stream.Read(buf); err != nil {
			t.Fatal(err)
		} else {
			received += string(buf[:n])
		}
	}
	if sent != received {
		t.Fatal("data mimatch")
	}
	session.Close()
}

func TestSpeed(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	t.Log(stream.LocalAddr(), stream.RemoteAddr())

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		buf := make([]byte, 1024*1024)
		nrecv := 0
		for {
			n, err := stream.Read(buf)
			if err != nil {
				t.Fatal(err)
				break
			} else {
				nrecv += n
				if nrecv == 4096*4096 {
					break
				}
			}
		}
		stream.Close()
		t.Log("time for 16MB rtt", time.Since(start))
		wg.Done()
	}()
	msg := make([]byte, 8192)
	for i := 0; i < 2048; i++ {
		stream.Write(msg)
	}
	wg.Wait()
	session.Close()
}

func TestParallel(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)

	par := 1000
	messages := 100
	var wg sync.WaitGroup
	wg.Add(par)
	for i := 0; i < par; i++ {
		stream, _ := session.OpenStream()
		go func(s *Stream) {
			buf := make([]byte, 20)
			for j := 0; j < messages; j++ {
				msg := fmt.Sprintf("hello%v", j)
				s.Write([]byte(msg))
				if _, err := s.Read(buf); err != nil {
					break
				}
			}
			s.Close()
			wg.Done()
		}(stream)
	}
	t.Log("created", session.NumStreams(), "streams")
	wg.Wait()
	session.Close()
}

func TestCloseThenOpen(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	session.Close()
	if _, err := session.OpenStream(); err == nil {
		t.Fatal("opened after close")
	}
}

func TestStreamDoubleClose(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	stream.Close()
	if err := stream.Close(); err == nil {
		t.Log("double close doesn't return error")
	}
	session.Close()
}

func TestConcurrentClose(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	numStreams := 100
	streams := make([]*Stream, 0, numStreams)
	var wg sync.WaitGroup
	wg.Add(numStreams)
	for i := 0; i < 100; i++ {
		stream, _ := session.OpenStream()
		streams = append(streams, stream)
	}
	for _, s := range streams {
		stream := s
		go func() {
			stream.Close()
			wg.Done()
		}()
	}
	session.Close()
	wg.Wait()
}

func TestTinyReadBuffer(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	const N = 100
	tinybuf := make([]byte, 6)
	var sent string
	var received string
	for i := 0; i < N; i++ {
		msg := fmt.Sprintf("hello%v", i)
		sent += msg
		nsent, err := stream.Write([]byte(msg))
		if err != nil {
			t.Fatal("cannot write")
		}
		nrecv := 0
		for nrecv < nsent {
			if n, err := stream.Read(tinybuf); err == nil {
				nrecv += n
				received += string(tinybuf[:n])
			} else {
				t.Fatal("cannot read with tiny buffer")
			}
		}
	}

	if sent != received {
		t.Fatal("data mimatch")
	}
	session.Close()
}

func TestIsClose(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	session.Close()
	if session.IsClosed() != true {
		t.Fatal("still open after close")
	}
}

func TestKeepAliveTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:29999")
	if err != nil {
		// handle error
		panic(err)
	}
	go func() {
		ln.Accept()
	}()

	cli, err := net.Dial("tcp", "127.0.0.1:29999")
	if err != nil {
		t.Fatal(err)
	}

	config := DefaultConfig()
	config.KeepAliveInterval = time.Second
	config.KeepAliveTimeout = 2 * time.Second
	session, _ := Client(cli, config)
	<-time.After(3 * time.Second)
	if session.IsClosed() != true {
		t.Fatal("keepalive-timeout failed")
	}
}

func TestServerEcho(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:39999")
	if err != nil {
		// handle error
		panic(err)
	}
	go func() {
		if conn, err := ln.Accept(); err == nil {
			session, _ := Server(conn, nil)
			if stream, err := session.OpenStream(); err == nil {
				const N = 100
				buf := make([]byte, 10)
				for i := 0; i < N; i++ {
					msg := fmt.Sprintf("hello%v", i)
					stream.Write([]byte(msg))
					if n, err := stream.Read(buf); err != nil {
						t.Fatal(err)
					} else if string(buf[:n]) != msg {
						t.Fatal(err)
					}
				}
				stream.Close()
			} else {
				t.Fatal(err)
			}
		} else {
			t.Fatal(err)
		}
	}()

	cli, err := net.Dial("tcp", "127.0.0.1:39999")
	if err != nil {
		t.Fatal(err)
	}
	if session, err := Client(cli, nil); err == nil {
		if stream, err := session.AcceptStream(); err == nil {
			buf := make([]byte, 65536)
			for {
				n, err := stream.Read(buf)
				if err != nil {
					break
				}
				stream.Write(buf[:n])
			}
		} else {
			t.Fatal(err)
		}
	} else {
		t.Fatal(err)
	}
}

func TestSendWithoutRecv(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	const N = 100
	for i := 0; i < N; i++ {
		msg := fmt.Sprintf("hello%v", i)
		stream.Write([]byte(msg))
	}
	buf := make([]byte, 1)
	if _, err := stream.Read(buf); err != nil {
		t.Fatal(err)
	}
	stream.Close()
}

func TestWriteAfterClose(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	stream.Close()
	if _, err := stream.Write([]byte("write after close")); err == nil {
		t.Fatal("write after close failed")
	}
}

func TestReadStreamAfterSessionClose(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	session.Close()
	buf := make([]byte, 10)
	if _, err := stream.Read(buf); err != nil {
		t.Log(err)
	} else {
		t.Fatal("read stream after session close succeeded")
	}
}

func TestWriteStreamAfterConnectionClose(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	session.conn.Close()
	if _, err := stream.Write([]byte("write after connection close")); err == nil {
		t.Fatal("write after connection close failed")
	}
}

func TestNumStreamAfterClose(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	if _, err := session.OpenStream(); err == nil {
		if session.NumStreams() != 1 {
			t.Fatal("wrong number of streams after opened")
		}
		session.Close()
		if session.NumStreams() != 0 {
			t.Fatal("wrong number of streams after session closed")
		}
	} else {
		t.Fatal(err)
	}
	cli.Close()
}

func TestRandomFrame(t *testing.T) {
	// pure random
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	for i := 0; i < 100; i++ {
		rnd := make([]byte, randUint32()%1024)
		io.ReadFull(rand.Reader, rnd)
		session.conn.Write(rnd)
	}
	cli.Close()

	// double syn
	cli, err = net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(cmdSYN, 1000)
		session.writeFrame(f, time.Time{})
	}
	cli.Close()

	// random cmds
	cli, err = net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	allcmds := []byte{cmdSYN, cmdFIN, cmdPSH, cmdNOP}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(allcmds[randUint32()%uint32(len(allcmds))], randUint32())
		session.writeFrame(f, time.Time{})
	}
	cli.Close()

	// random cmds & sids
	cli, err = net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(byte(randUint32()), randUint32())
		session.writeFrame(f, time.Time{})
	}
	cli.Close()

	// random version
	cli, err = net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(byte(randUint32()), randUint32())
		f.ver = byte(randUint32())
		session.writeFrame(f, time.Time{})
	}
	cli.Close()

	// incorrect size
	cli, err = net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)

	f := newFrame(byte(randUint32()), randUint32())
	rnd := make([]byte, randUint32()%1024)
	io.ReadFull(rand.Reader, rnd)
	f.data = rnd

	buf := make([]byte, headerSize+len(f.data))
	buf[0] = f.ver
	buf[1] = f.cmd
	binary.LittleEndian.PutUint16(buf[2:], uint16(len(rnd)+1)) /// incorrect size
	binary.LittleEndian.PutUint32(buf[4:], f.sid)
	copy(buf[headerSize:], f.data)

	session.conn.Write(buf)
	t.Log(rawHeader(buf))
	cli.Close()
}

func TestReadDeadline(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	const N = 100
	buf := make([]byte, 10)
	var readErr error
	for i := 0; i < N; i++ {
		msg := fmt.Sprintf("hello%v", i)
		stream.Write([]byte(msg))
		stream.SetReadDeadline(time.Now().Add(-1 * time.Minute))
		if _, readErr = stream.Read(buf); readErr != nil {
			break
		}
	}
	if readErr != nil {
		if !strings.Contains(readErr.Error(), "i/o timeout") {
			t.Fatalf("Wrong error: %v", readErr)
		}
	} else {
		t.Fatal("No error when reading with past deadline")
	}
	session.Close()
}

func TestWriteDeadline(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	buf := make([]byte, 10)
	var writeErr error
	for {
		stream.SetWriteDeadline(time.Now().Add(-1 * time.Minute))
		if _, writeErr = stream.Write(buf); writeErr != nil {
			if !strings.Contains(writeErr.Error(), "i/o timeout") {
				t.Fatalf("Wrong error: %v", writeErr)
			}
			break
		}
	}
	session.Close()
}

func TestKeepAlive(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	config := DefaultConfig()
	config.KeepAliveInterval = 1 * time.Second
	config.KeepAliveTimeout = 3 * time.Second

	srvListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srvListener.Close()
	go func() {
		conn, err := srvListener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		srv, err := Server(conn, config)
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()
		for i := 0; i < 2; i++ {
			stream, err := srv.AcceptStream()
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			// echo until Read fails
			for {
				buf := make([]byte, 65536)
				n, err := stream.Read(buf)
				if err != nil {
					break
				}
				stream.Write(buf[:n])
			}
		}
	}()

	cliConn, err := net.Dial("tcp", srvListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cli, err := Client(cliConn, config)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	time.Sleep(10 * time.Second)

	cliStream, err := cli.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	defer cliStream.Close()

	// perform an echo exchange
	const echoString = "hello world"
	_, err = cliStream.Write([]byte(echoString))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(echoString))
	_, err = io.ReadFull(cliStream, buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != echoString {
		t.Fatal("bad echo:", string(buf))
	}

	// wait for a few keepalive pings
	time.Sleep(10 * time.Second)

	// perform another echo exchange. The stream should still be open.
	_, err = cliStream.Write([]byte(echoString))
	if err != nil {
		t.Fatal(err)
	}
	buf = make([]byte, len(echoString))
	_, err = io.ReadFull(cliStream, buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != echoString {
		t.Fatal("bad echo:", string(buf))
	}

	// close the stream, wait for a few keepalive pings, then open a new
	// stream
	err = cliStream.Close()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Second)

	cliStream, err = cli.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	defer cliStream.Close()

	// perform an echo exchange
	_, err = cliStream.Write([]byte(echoString))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadFull(cliStream, buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != echoString {
		t.Fatal("bad echo:", string(buf))
	}
	cliStream.Close()
}

type slowWriterConn struct {
	net.Conn
	writeTime time.Duration
}

func (c slowWriterConn) Write(p []byte) (int, error) {
	time.Sleep(c.writeTime)
	return c.Conn.Write(p)
}

func TestKeepAliveSlowServer(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	config := DefaultConfig()
	config.KeepAliveInterval = 1 * time.Second
	config.KeepAliveTimeout = 3 * time.Second

	srvListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srvListener.Close()
	go func() {
		conn, err := srvListener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		slowConn := slowWriterConn{
			Conn:      conn,
			writeTime: 5 * time.Second,
		}
		srv, err := Server(slowConn, config)
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()
		for {
			_, err := srv.AcceptStream()
			if err != nil {
				break
			}
		}
	}()

	cliConn, err := net.Dial("tcp", srvListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cli, err := Client(cliConn, config)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	// open a stream
	_, err = cli.OpenStream()
	if err != nil {
		t.Fatal(err)
	}

	// wait for keepalive to fail
	time.Sleep(5 * time.Second)

	// try to open a stream
	_, err = cli.OpenStream()
	if err != errBrokenPipe {
		t.Fatal("OpenStream should have failed with errBrokenPipe; got", err)
	}
}

func TestStreamDeadlineSlowServer(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	config := DefaultConfig()
	config.KeepAliveInterval = 1 * time.Second
	config.KeepAliveTimeout = 3 * time.Second

	srvListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srvListener.Close()
	go func() {
		conn, err := srvListener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		srv, err := Server(conn, config)
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()
		for {
			// accept stream, but perform no further I/O
			_, err := srv.AcceptStream()
			if err != nil {
				break
			}
		}
	}()

	cliConn, err := net.Dial("tcp", srvListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cli, err := Client(cliConn, config)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	// open a stream
	stream, err := cli.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	// set deadline and attempt a read
	stream.SetReadDeadline(time.Now().Add(time.Second))
	_, err = stream.Read(make([]byte, 10))
	if err != errTimeout {
		t.Fatal("excepted errTimeout, got", err)
	}
}

func BenchmarkAcceptClose(b *testing.B) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		b.Fatal(err)
	}
	session, _ := Client(cli, nil)
	for i := 0; i < b.N; i++ {
		if stream, err := session.OpenStream(); err == nil {
			stream.Close()
		} else {
			b.Fatal(err)
		}
	}
}
func BenchmarkConnSmux(b *testing.B) {
	cs, ss, err := getSmuxStreamPair()
	if err != nil {
		b.Fatal(err)
	}
	defer cs.Close()
	defer ss.Close()
	bench(b, cs, ss)
}

func BenchmarkConnTCP(b *testing.B) {
	cs, ss, err := getTCPConnectionPair()
	if err != nil {
		b.Fatal(err)
	}
	defer cs.Close()
	defer ss.Close()
	bench(b, cs, ss)
}

func getSmuxStreamPair() (*Stream, *Stream, error) {
	c1, c2, err := getTCPConnectionPair()
	if err != nil {
		return nil, nil, err
	}

	s, err := Server(c2, nil)
	if err != nil {
		return nil, nil, err
	}
	c, err := Client(c1, nil)
	if err != nil {
		return nil, nil, err
	}
	var ss *Stream
	done := make(chan error)
	go func() {
		var rerr error
		ss, rerr = s.AcceptStream()
		done <- rerr
		close(done)
	}()
	cs, err := c.OpenStream()
	if err != nil {
		return nil, nil, err
	}
	err = <-done
	if err != nil {
		return nil, nil, err
	}

	return cs, ss, nil
}

func getTCPConnectionPair() (net.Conn, net.Conn, error) {
	lst, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}

	var conn0 net.Conn
	var err0 error
	done := make(chan struct{})
	go func() {
		conn0, err0 = lst.Accept()
		close(done)
	}()

	conn1, err := net.Dial("tcp", lst.Addr().String())
	if err != nil {
		return nil, nil, err
	}

	<-done
	if err0 != nil {
		return nil, nil, err0
	}
	return conn0, conn1, nil
}

func bench(b *testing.B, rd io.Reader, wr io.Writer) {
	buf := make([]byte, 128*1024)
	buf2 := make([]byte, 128*1024)
	b.SetBytes(128 * 1024)
	b.ResetTimer()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		count := 0
		for {
			n, _ := rd.Read(buf2)
			count += n
			if count == 128*1024*b.N {
				return
			}
		}
	}()
	for i := 0; i < b.N; i++ {
		wr.Write(buf)
	}
	wg.Wait()
}
