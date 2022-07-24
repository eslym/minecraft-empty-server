package main

import (
	"container/list"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Tnze/go-mc/chat"
	"github.com/Tnze/go-mc/data/packetid"
	"github.com/Tnze/go-mc/level"
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
	Description chat.Message  `json:"description"`
	FavIcon     string        `json:"favicon,omitempty"`
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
	Welcome     string
)

func init() {
	flag.StringVar(&BindAddress, "bind", "0.0.0.0:25565", "The address which this server listening to")
	flag.BoolVar(&EnableProxy, "proxy", false, "Accept PROXY header")
	flag.StringVar(&MOTD, "motd", "An empty Minecraft Server", "MOTD for ping")
	flag.StringVar(&Welcome, "welcome", "Welcome to minecraft empty server", "Welcome message in chat")
	flag.IntVar(&MaxPlayer, "max", 0, "Max player can join this server, 0 or less for unlimited.")
	flag.BoolVar(&PrintHelp, "help", false, "Print help")
}

func main() {
	flag.Parse()

	if PrintHelp {
		_, _ = fmt.Fprintln(os.Stderr, "Usage:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	rand.Seed(time.Now().UnixNano())

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
		Description: chat.Message{Text: MOTD},
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

	err = conn.WritePacket(packet.Marshal(
		PacketLoginResponse,
		packet.UUID(playerUuid),
		username,
		packet.VarInt(0),
	))

	if err != nil {
		return
	}

	log.Printf("%s(%s) connected", username, playerUuid.String())

	ConnectedPlayers.PushBack(conn)

	defer log.Printf("%s(%s) disconnected", username, playerUuid.String())

	_ = conn.WritePacket(packet.Marshal(
		packetid.ClientboundLogin,
		packet.Int(100),        // Entity ID
		packet.Boolean(false),  // Hardcore
		packet.UnsignedByte(3), // Game mode
		packet.Byte(-1),        // Previous game mode
		packet.Array([]packet.Identifier{
			"minecraft:overworld",
			"minecraft:nether",
			"minecraft:the_end",
			"minecraft:overworld_caves",
		}), // Dimensions
		packet.NBT(nbt.StringifiedMessage(dimensionCodecSNBT)), // Dimension codec
		packet.Identifier("minecraft:the_end"),                 // Dimension type
		packet.Identifier("minecraft:temp"),                    // Dimension
		packet.Long(rand.Int63()),                              // Seed
		packet.VarInt(MaxPlayer),                               // Max players
		packet.VarInt(1),                                       // View distance
		packet.VarInt(1),                                       // Simulation distance
		packet.Boolean(false),                                  // Reduce debug info
		packet.Boolean(true),                                   // Enable respawn screen
		packet.Boolean(true),                                   // Is debug
		packet.Boolean(true),                                   // Is flat
		packet.Boolean(false),                                  // Has death location
	))

	_ = conn.WritePacket(packet.Marshal(
		packetid.ClientboundCustomPayload,
		packet.String("minecraft:brand"),
		packet.String("golang server"),
	))

	_ = conn.WritePacket(packet.Marshal(
		packetid.ClientboundChangeDifficulty,
		packet.UnsignedByte(0),
		packet.Boolean(true),
	))

	_ = conn.WritePacket(packet.Marshal(
		packetid.ClientboundPlayerAbilities,
		packet.Byte(7),
		packet.Float(0.05),
		packet.Float(0.1),
	))

	_, _ = writePosition(conn, 8, 60, 8)

	_ = conn.WritePacket(packet.Marshal(
		packetid.ClientboundPlayerInfo,
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

	_ = conn.WritePacket(packet.Marshal(
		packetid.ClientboundSetChunkCacheCenter,
		packet.VarInt(0),
		packet.VarInt(0),
	))

	_ = writeChunk(conn, 0, 0, 256)

	_ = conn.WritePacket(packet.Marshal(
		packetid.ClientboundSetDefaultSpawnPosition,
		packet.Position{X: 8, Y: 60, Z: 8},
		packet.Float(0),
	))

	_, _ = writePosition(conn, 8, 60, 8)

	msg := chat.Message{Text: Welcome, Color: chat.Yellow}
	serialized, _ := json.Marshal(msg)

	_ = conn.WritePacket(packet.Marshal(
		packetid.ClientboundSystemChat,
		packet.String(serialized),
		packet.VarInt(1),
	))

	ticker := time.NewTicker(time.Second)

	go func(conn *net.Conn, ticker *time.Ticker) {
		for range ticker.C {
			_ = conn.WritePacket(packet.Marshal(packetid.ClientboundKeepAlive, packet.Long(rand.Int63())))
			_, _ = writePosition(conn, 0, 0, 0)
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
	msg := chat.Message{Text: message}
	serialized, _ := json.Marshal(msg)
	kick := packet.Marshal(PacketKickLogin, packet.String(serialized))
	_ = conn.WritePacket(kick)
}

func writePosition(conn *net.Conn, x float64, y float64, z float64) (int32, error) {
	teleportId := rand.Int31()
	return teleportId, conn.WritePacket(packet.Marshal(
		packetid.ClientboundPlayerPosition,
		packet.Double(x), packet.Double(y), packet.Double(z),
		packet.Float(0.0), packet.Float(0.0),
		packet.Byte(0), packet.VarInt(teleportId), packet.Boolean(true),
	))
}

func writeChunk(conn *net.Conn, x int, z int, height int) error {
	chunk := level.EmptyChunk(height)
	data, _ := chunk.Data()
	return conn.WritePacket(packet.Marshal(
		packetid.ClientboundLevelChunkWithLight,
		packet.Int(x), packet.Int(z),
		packet.NBT(nbt.StringifiedMessage("{}")),
		packet.ByteArray(data),
		packet.ByteArray{}, // pretending block entity array
		packet.Boolean(false),
		packet.BitSet{},
		packet.BitSet{},
		packet.BitSet{},
		packet.BitSet{},
		packet.ByteArray{},
		packet.ByteArray{},
	))
}
