package main

import (
	"agones.dev/agones/pkg/util/signals"
	sdk "agones.dev/agones/sdks/go"
	"bufio"
	"context"
	"flag"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

func doHealth(sdk *sdk.SDK, ctx context.Context) {
	tick := time.Tick(2 * time.Second)
	for {
		log.Printf("Health Ping")
		err := sdk.Health()
		if err != nil {
			log.Fatalf("Could not send health ping, %v", err)
		}
		select {
		case <-ctx.Done():
			log.Print("Stopped health pings")
			return
		case <-tick:
		}
	}
}

func main() {
	sigCtx, _ := signals.NewSigKillContext()

	port := flag.String("port", "9123", "port to serve on")
	readyOnStart := flag.Bool("ready", true, "Mark this GameServer as Ready on startup")
	readyDelaySec := flag.Int("readyDelaySec", 0, "If greater than zero, wait this many seconds each time before marking the game server as ready")
	gracefulTerminationDelaySec := flag.Int("gracefulTerminationDelaySec", 0, "Delay after we've been asked to terminate (by SIGKILL or automaticShutdownDelaySec)")
	udp := flag.Bool("udp", true, "Server will listen on UDP")
	tcp := flag.Bool("tcp", false, "Server will listen on TCP")

	flag.Parse()
	if ep := os.Getenv("PORT"); ep != "" {
		port = &ep
	}
	if eready := os.Getenv("READY"); eready != "" {
		r := strings.ToUpper(eready) == "TRUE"
		readyOnStart = &r
	}
	if eudp := os.Getenv("UDP"); eudp != "" {
		u := strings.ToUpper(eudp) == "TRUE"
		udp = &u
	}
	if etcp := os.Getenv("TCP"); etcp != "" {
		t := strings.ToUpper(etcp) == "TRUE"
		tcp = &t
	}

	log.Print("Creating SDK instance")
	s, err := sdk.NewSDK()
	if err != nil {
		log.Fatalf("Could not connect to sdk: %v", err)
	}

	log.Print("Starting Health Ping")
	ctx, cancel := context.WithCancel(context.Background())
	go doHealth(s, ctx)

	if *tcp {
		go tcpListener(port, s, cancel)
	}

	if *udp {
		go udpListener(port, s, cancel)
	}

	if *readyOnStart {
		if *readyDelaySec > 0 {
			log.Printf("Waiting %d seconds before moving to ready", *readyDelaySec)
			time.Sleep(time.Duration(*readyDelaySec) * time.Second)
		}
		log.Print("Marking this server as ready")
		err := s.Ready()
		if err != nil {
			log.Fatalf("Could not send ready message")
		}
	}

	<-sigCtx.Done()
	log.Printf("Waiting %d seconds before exiting", *gracefulTerminationDelaySec)
	time.Sleep(time.Duration(*gracefulTerminationDelaySec) * time.Second)
	os.Exit(0)
}

func udpListener(port *string, s *sdk.SDK, cancel context.CancelFunc) {
	log.Printf("Starting UDP server, listening on port %s", *port)
	conn, err := net.ListenPacket("udp", ":"+*port)
	if err != nil {
		log.Fatalf("Could not start UDP server: %v", err)
	}
	defer conn.Close() // nolint: errcheck
	udpReadWriteLoop(conn, cancel, s)
}

func udpReadWriteLoop(conn net.PacketConn, cancel context.CancelFunc, s *sdk.SDK) {
	b := make([]byte, 1024)
	for {
		sender, txt := readPacket(conn, b)

		log.Printf("Received UDP: %v", txt)

		response, addACK, err := handleResponse(txt, s, cancel)
		if err != nil {
			response = "ERROR: " + response + "\n"
		} else if addACK {
			response = "ACK: " + response + "\n"
		}

		udpRespond(conn, sender, response)

		if txt == "EXIT" {
			exit(s)
		}
	}
}

func udpRespond(conn net.PacketConn, sender net.Addr, txt string) {
	if _, err := conn.WriteTo([]byte(txt), sender); err != nil {
		log.Fatalf("Could not write to udp stream: %v", err)
	}
}

func tcpListener(port *string, s *sdk.SDK, cancel context.CancelFunc) {
	log.Printf("Starting TCP server, listening on port %s", *port)
	ln, err := net.Listen("tcp", ":"+*port)
	if err != nil {
		log.Fatalf("Could not start TCP server: %v", err)
	}
	defer ln.Close() // nolint: errcheck

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("Unable to accept incoming TCP connection: %v", err)
		}
		go tcpHandleConnection(conn, s, cancel)
	}
}

// handleConnection services a single tcp connection to the server
func tcpHandleConnection(conn net.Conn, s *sdk.SDK, cancel context.CancelFunc) {
	log.Printf("TCP Client %s connected", conn.RemoteAddr().String())
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		tcpHandleCommand(conn, scanner.Text(), s, cancel)
	}
	log.Printf("TCP Client %s disconnected", conn.RemoteAddr().String())
}

func tcpHandleCommand(conn net.Conn, txt string, s *sdk.SDK, cancel context.CancelFunc) {
	log.Printf("TCP txt: %v", txt)

	response, addACK, err := handleResponse(txt, s, cancel)
	if err != nil {
		response = "ERROR: " + response + "\n"
	} else if addACK {
		response = "ACK TCP: " + response + "\n"
	}

	tcpRespond(conn, response)

	if response == "EXIT" {
		exit(s)
	}
}

// respond responds to a given sender.
func tcpRespond(conn net.Conn, txt string) {
	log.Printf("Responding to TCP with %q", txt)
	if _, err := conn.Write([]byte(txt + "\n")); err != nil {
		log.Fatalf("Could not write to TCP stream: %v", err)
	}
}

// readPacket reads a string from the connection
func readPacket(conn net.PacketConn, b []byte) (net.Addr, string) {
	n, sender, err := conn.ReadFrom(b)
	if err != nil {
		log.Fatalf("Could not read from udp stream: %v", err)
	}
	txt := strings.TrimSpace(string(b[:n]))
	log.Printf("Received packet from %v: %v", sender.String(), txt)
	return sender, txt
}
