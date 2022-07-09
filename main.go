package main

import (
	"container/list"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Tnze/go-mc/nbt"
	"github.com/Tnze/go-mc/net"
	"github.com/Tnze/go-mc/net/packet"
	"github.com/Tnze/go-mc/offline"
	"go/types"
	"log"
	"math/rand"
	"os"
	"time"
)

type PingPlayers struct {
	Max    int         `json:"max"`
	Online int         `json:"online"`
	Sample []types.Nil `json:"sample"`
}

type ServerVersion struct {
	Name     string `json:"name"`
	Protocol int    `json:"protocol"`
}

type PingResponse struct {
	Version     ServerVersion `json:"version"`
	Players     PingPlayers   `json:"players"`
	Description Message       `json:"description"`
	FavIcon     string        `json:"favicon,omitempty"`
}

type Message struct {
	Text string `json:"text"`
}

const (
	ProtocolVersion = 759

	PacketHandshake = 0x00

	PacketStatusRequest  = 0x00
	PacketStatusResponse = 0x00
	PacketPingRequest    = 0x01
	PacketPingResponse   = 0x01

	PacketLoginStart    = 0x00
	PacketKickLogin     = 0x00
	PacketLoginResponse = 0x02

	PacketLoginPlay       = 0x23
	PacketPlayerAbilities = 0x2F
	PacketSyncPosition    = 0x36
	PacketEntityEvent     = 0x18
	PacketPlayerInfo      = 0x34
	PacketSpawnLocation   = 0x4A

	PacketKeepalive = 0x1E
)

var ConnectedPlayers = list.New()

//go:embed res/codec759.snbt
var dimensionCodecSNBT string

var (
	BindAddress string
	EnableProxy bool
	PrintHelp   bool
	MaxPlayer   int
	MOTD        string
)

func init() {
	flag.StringVar(&BindAddress, "bind", "0.0.0.0:25565", "The address which this server listening to")
	flag.BoolVar(&EnableProxy, "proxy", false, "Accept PROXY header")
	flag.StringVar(&MOTD, "motd", "An empty Minecraft Server", "MOTD for ping")
	flag.IntVar(&MaxPlayer, "max", 0, "Max player can join this server, 0 or less for unlimited.")
	flag.BoolVar(&PrintHelp, "help", false, "Print help")
}

func main() {
	if PrintHelp {
		_, _ = fmt.Fprintln(os.Stderr, "Usage:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	listener, err := net.ListenMC(BindAddress)
	if err != nil {
		log.Fatalln("Failed to listen")
	}

	log.Println("Listening on " + BindAddress)

	defer func(listener *net.Listener) {
		_ = listener.Close()
	}(listener)

	for {
		conn, _ := listener.Accept()
		go handleConnection(&conn)
	}
}

func handleConnection(conn *net.Conn) {
	defer func(conn *net.Conn) {
		_ = conn.Close()
		var next *list.Element
		for e := ConnectedPlayers.Front(); e != nil; e = next {
			next = e.Next()
			if conn == e.Value {
				ConnectedPlayers.Remove(e)
			}
		}
	}(conn)

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

func handlePing(conn *net.Conn) {
	var p packet.Packet
	err := conn.ReadPacket(&p)
	if err != nil || p.ID != PacketStatusRequest {
		return
	}

	status := PingResponse{
		Version: ServerVersion{
			Name:     "1.19",
			Protocol: ProtocolVersion,
		},
		Players: PingPlayers{
			Max:    MaxPlayer,
			Online: ConnectedPlayers.Len(),
			Sample: []types.Nil{},
		},
		Description: Message{Text: MOTD},
	}

	serialized, _ := json.Marshal(status)
	res := packet.Marshal(PacketStatusResponse, packet.String(serialized))
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

func handleLogin(conn *net.Conn, protocol int) {
	if protocol != ProtocolVersion {
		loginKick(conn, "Unsupported version")
		return
	}

	var pack packet.Packet
	err := conn.ReadPacket(&pack)
	if err != nil || pack.ID != PacketLoginStart {
		return
	}

	var (
		username   packet.String
		hasSig     packet.Boolean
		keyExpires packet.Long
		pubKey     packet.ByteArray
		signature  packet.ByteArray
	)

	err = pack.Scan(&username, &hasSig)
	if err != nil {
		return
	}

	if hasSig {
		err = pack.Scan(&username, &hasSig, &keyExpires, &pubKey, &signature)
		if err != nil {
			return
		}
		// No documentation for how to handle this
	}
	playerUuid := offline.NameToUUID(string(username))

	success := packet.Marshal(PacketLoginResponse, packet.UUID(playerUuid), username, packet.VarInt(0))

	err = conn.WritePacket(success)

	if err != nil {
		return
	}

	log.Printf("%s(%s) connected", username, playerUuid.String())

	ConnectedPlayers.PushBack(conn)

	defer log.Printf("%s(%s) disconnected", username, playerUuid.String())

	_ = conn.WritePacket(packet.Marshal(
		PacketLoginPlay,
		packet.Int(100),        // Entity ID
		packet.Boolean(false),  // Hardcore
		packet.UnsignedByte(3), // Game mode
		packet.Byte(-1),        // Previous game mode
		packet.Array([]packet.Identifier{"world", "nether", "end"}), // Dimensions
		packet.NBT(nbt.StringifiedMessage(dimensionCodecSNBT)),      // Dimension codec
		packet.Identifier("minecraft:the_end"),                      // Dimension type
		packet.Identifier("end"),                                    // Dimension
		packet.Long(rand.Int63()),                                   // Seed
		packet.VarInt(MaxPlayer),                                    // Max players
		packet.VarInt(1),                                            // View distance
		packet.VarInt(1),                                            // Simulation distance
		packet.Boolean(false),                                       // Reduce debug info
		packet.Boolean(true),                                        // Enable respawn screen
		packet.Boolean(false),                                       // Is debug
		packet.Boolean(true),                                        // Is flat
		packet.Boolean(false),                                       // Has death location
	))

	_ = conn.WritePacket(packet.Marshal(
		PacketPlayerAbilities,
		packet.Byte(7),
		packet.Float(0.05),
		packet.Float(0.1),
	))

	_ = conn.WritePacket(packet.Marshal(
		PacketEntityEvent,
		packet.Int(100),
		packet.Byte(24),
	))

	_ = conn.WritePacket(packet.Marshal(
		PacketSpawnLocation,
		packet.Position{X: 0, Y: 60, Z: 0},
		packet.Float(0),
	))

	_ = writePosition(conn, 0, 60, 0)

	_ = conn.WritePacket(packet.Marshal(
		PacketPlayerInfo,
		packet.VarInt(0),
		packet.VarInt(1),
		packet.UUID(playerUuid),
		username,
		packet.VarInt(0),
		packet.VarInt(3),
		packet.VarInt(0),
		packet.Boolean(false),
		packet.Boolean(false),
	))

	_ = writePosition(conn, 0, 60, 0)

	ticker := time.NewTicker(time.Second)

	go func(conn *net.Conn, ticker *time.Ticker) {
		for range ticker.C {
			_ = conn.WritePacket(packet.Marshal(PacketKeepalive, packet.Long(rand.Int63())))
			_ = writePosition(conn, 0, 60, 0)
		}
	}(conn, ticker)

	for {
		err = conn.ReadPacket(&pack)
		if err != nil {
			ticker.Stop()
			return
		}
	}
}

func loginKick(conn *net.Conn, message string) {
	msg := Message{Text: message}
	serialized, _ := json.Marshal(msg)
	kick := packet.Marshal(PacketKickLogin, packet.String(serialized))
	_ = conn.WritePacket(kick)
}

func writePosition(conn *net.Conn, x float64, y float64, z float64) error {
	return conn.WritePacket(packet.Marshal(
		PacketSyncPosition,
		packet.Double(x), packet.Double(y), packet.Double(z),
		packet.Float(0.0), packet.Float(0.0),
		packet.Byte(0), packet.VarInt(rand.Int()), packet.Boolean(true),
	))
}
