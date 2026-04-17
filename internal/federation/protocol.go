// Package federation implements server-to-server peering for the Brew protocol.
//
// Federation allows independent Brew servers to share subscriber registrations,
// talkgroup affiliations, and route calls between each other — creating a
// decentralized TETRA network with no single point of failure.
package federation

// Federation message types (JSON text frames over WebSocket)
const (
	MsgHello            = "hello"             // Initial handshake
	MsgSubscriberUpdate = "subscriber_update" // ISSI registered/deregistered
	MsgAffiliateUpdate  = "affiliate_update"  // ISSI affiliated/deaffiliated to GSSI
	MsgCallStart        = "call_start"        // GROUP_TX from peer
	MsgCallEnd          = "call_end"          // GROUP_IDLE from peer
	MsgCallFrame        = "call_frame"        // Voice frame (binary, not JSON)
	MsgSDSRelay         = "sds_relay"         // SDS message relay
	MsgSyncRequest      = "sync_request"      // Request full subscriber table
	MsgSyncResponse     = "sync_response"     // Full subscriber table
	MsgPeerExchange     = "peer_exchange"     // Exchange known peer URLs (gossip)
	MsgUsersDBOffer     = "usersdb_offer"     // Advertise users.txt timestamp
	MsgUsersDBRequest   = "usersdb_request"   // Request users.txt from peer
)

// ProtocolVersion is the federation protocol version.
const ProtocolVersion = 2

// MaxTTL is the maximum number of hops a message can travel.
const MaxTTL = 10

// Message is the envelope for all federation JSON messages.
type Message struct {
	Type    string `json:"type"`
	Origin  string `json:"origin"`  // Server name that originated this message
	Version int    `json:"version,omitempty"`

	// Mesh relay fields
	MsgID string   `json:"msg_id,omitempty"` // Unique message ID for deduplication
	TTL   int      `json:"ttl,omitempty"`    // Hops remaining (decremented at each relay)
	Path  []string `json:"path,omitempty"`   // Servers this message has passed through

	// Subscriber/Affiliate updates
	ISSI   uint32   `json:"issi,omitempty"`
	Action string   `json:"action,omitempty"` // "register", "deregister", "affiliate", "deaffiliate"
	GSSIs  []uint32 `json:"gssis,omitempty"`

	// Call control
	UUID       string `json:"uuid,omitempty"`
	SourceISSI uint32 `json:"source_issi,omitempty"`
	DestGSSI   uint32 `json:"dest_gssi,omitempty"`
	Priority   uint8  `json:"priority,omitempty"`
	Service    uint16 `json:"service,omitempty"`
	Cause      uint8  `json:"cause,omitempty"`

	// SDS relay
	DestISSI uint32 `json:"dest_issi,omitempty"`
	SDSData  string `json:"sds_data,omitempty"` // hex-encoded

	// Sync
	Subscribers map[string]SyncSubscriber `json:"subscribers,omitempty"`

	// Peer exchange (gossip)
	Peers []GossipPeer `json:"peers,omitempty"`

	// Users DB sync
	UsersDBTimestamp string `json:"usersdb_timestamp,omitempty"`
	UsersDBURL       string `json:"usersdb_url,omitempty"`
	UsersDBCount     int    `json:"usersdb_count,omitempty"`
}

// GossipPeer is a known peer advertised during peer exchange.
type GossipPeer struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// SyncSubscriber is a subscriber entry in a sync response.
type SyncSubscriber struct {
	GSSIs    []uint32 `json:"gssis"`
	Callsign string   `json:"callsign,omitempty"`
}
