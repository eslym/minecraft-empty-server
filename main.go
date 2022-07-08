package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Tnze/go-mc/net"
	"github.com/Tnze/go-mc/net/packet"
	"go/types"
	"log"
	"os"
)

const ProtocolVersion = 759

const (
	PacketHandshake = 0x00

	PacketStatusRequest  = 0x00
	PacketStatusResponse = 0x00
	PacketPingRequest    = 0x01
	PacketPingResponse   = 0x01

	PacketLoginStart = 0x00

	PacketKeepalive = 0x1E
)

var (
	BindAddress string
	EnableProxy bool
	PrintHelp   bool
	MOTD        string
	StaticPing  PingResponse
)

func init() {
	flag.StringVar(&BindAddress, "bind", "0.0.0.0:25565", "The address which this server listening to")
	flag.BoolVar(&EnableProxy, "proxy", false, "Accept PROXY header")
	flag.StringVar(&MOTD, "motd", "An empty Minecraft Server", "MOTD for ping")
	flag.BoolVar(&PrintHelp, "help", false, "Print help")
}

func main() {

	if PrintHelp {
		_, _ = fmt.Fprintln(os.Stderr, "Usage:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	StaticPing = PingResponse{
		Version: ServerVersion{
			Name:     "1.19",
			Protocol: ProtocolVersion,
		},
		Players: PingPlayers{
			Max:    0,
			Online: 0,
			Sample: []types.Nil{},
		},
		Description: Message{Text: MOTD},
	}

	listener, err := net.ListenMC(BindAddress)
	if err != nil {
		log.Fatalln("Failed to listen")
	}
	defer func(listener *net.Listener) {
		_ = listener.Close()
	}(listener)

	for {
		conn, _ := listener.Accept()
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer func(conn *net.Conn) {
		_ = conn.Close()
	}(&conn)

	if EnableProxy {
		var (
			netType    string
			remoteAddr string
			localAddr  string
			remotePort int
			localPort  int
		)
		_, err := fmt.Fscanf(
			conn, "PROXY %s %s %s %d %d\r\n",
			&netType, &remoteAddr, &localAddr, &remotePort, &localPort,
		)
		if err != nil {
			_ = conn.Close()
			return
		}
	}

	var (
		pack                packet.Packet
		Protocol, Intention packet.VarInt
		Address             packet.String
		Port                packet.UnsignedShort
	)

	err := conn.ReadPacket(&pack)

	if err != nil {
		return
	}

	if pack.ID != PacketHandshake {
		return
	}

	err = pack.Scan(&Protocol, &Address, &Port, &Intention)

	if err != nil {
		return
	}

	switch Intention {
	case 0x01:
		handlePing(conn)
	case 0x02:
		handleLogin(conn, int(Protocol))
	}
}

func handlePing(conn net.Conn) {
	var p packet.Packet
	err := conn.ReadPacket(&p)
	if err != nil || p.ID != PacketStatusRequest {
		return
	}
	status, _ := json.Marshal(StaticPing)
	res := packet.Marshal(PacketStatusResponse, packet.String(status))
	err = conn.WritePacket(res)
	if err != nil {
		return
	}
	err = conn.ReadPacket(&p)
	if err != nil || p.ID != PacketPingRequest {
		return
	}
	var ping packet.Long
	err = p.Scan(&ping)
	if err != nil {
		return
	}
	res = packet.Marshal(PacketPingResponse, ping)
	_ = conn.WritePacket(res)
}

func handleLogin(conn net.Conn, protocol int) {

}
