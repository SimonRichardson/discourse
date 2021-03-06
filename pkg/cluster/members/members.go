//go:generate mockgen -package=mocks -destination=./mocks/members.go github.com/SimonRichardson/alchemy/pkg/cluster/members Members,MemberList,Member

package members

import (
	"io"
	"strconv"
	"time"

	"github.com/pkg/errors"
)

// PeerType describes the type of peer with in the cluster.
type PeerType string

const (
	// PeerTypeUnknown defines a PeerType that is unknown in the cluster.
	PeerTypeUnknown PeerType = "peertype:unknown"
)

func (p PeerType) String() string {
	return string(p)
}

// EventHandler is an alias for the event dispatcher
type EventHandler interface {
	HandleEvent(Event) error
}

// EventBus allows the distributing and receiving of events over the cluster
type EventBus interface {

	// RegisterEventHandler attaches a event listener to all the members events
	// and broadcasts the event to the handler.
	RegisterEventHandler(EventHandler) error

	// DeregisterEventHandler removes the event listener.
	DeregisterEventHandler(EventHandler) error

	// DispatchEvent dispatches an event to all the members in the cluster.
	DispatchEvent(Event) error
}

// Members represents a way of joining a members cluster
type Members interface {
	EventBus

	// Join joins an existing members cluster. Returns the number of nodes
	// successfully contacted. The returned error will be non-nil only in the
	// case that no nodes could be contacted.
	Join() (int, error)

	// Leave gracefully exits the cluster. It is safe to call this multiple
	// times.
	Leave() error

	// Memberlist is used to get access to the underlying Memberlist instance
	MemberList() MemberList

	// Walk over a set of alive members
	Walk(func(PeerInfo) error) error

	// Close the current members cluster
	Close() error
}

// MemberList represents a way to manage members with in a cluster
type MemberList interface {

	// NumMembers returns the number of alive nodes currently known. Between
	// the time of calling this and calling Members, the number of alive nodes
	// may have changed, so this shouldn't be used to determine how many
	// members will be returned by Members.
	NumMembers() int

	// LocalNode is used to return the local Member
	LocalNode() Member

	// Members returns a point-in-time snapshot of the members of this cluster.
	Members() []Member
}

// Member represents a node in the cluster.
type Member interface {

	// Name returns the name of the member
	Name() string

	// Address returns the host:port of the member
	Address() string

	// PeerType returns the members PeerType
	PeerType() PeerType

	// Tags returns any associated tags of the member
	Tags() map[string]string
}

// Config defines a configuration setup for creating a list to manage the
// members cluster
type Config struct {
	peerType         PeerType
	nodeName         string
	apiAddr          string
	apiPort          int
	bindAddr         string
	bindPort         int
	advertiseAddr    string
	advertisePort    int
	clientAddr       string
	clientPort       int
	existing         []string
	logOutput        io.Writer
	broadcastTimeout time.Duration
}

// Option defines a option for generating a filesystem Config
type Option func(*Config) error

// Build ingests configuration options to then yield a Config and return an
// error if it fails during setup.
func Build(opts ...Option) (Config, error) {
	var config Config
	for _, opt := range opts {
		err := opt(&config)
		if err != nil {
			return Config{}, err
		}
	}
	return config, nil
}

// WithPeerType adds a PeerType to the configuration
func WithPeerType(peerType PeerType) Option {
	return func(config *Config) error {
		config.peerType = peerType
		return nil
	}
}

// WithNodeName adds a NodeName to the configuration
func WithNodeName(nodeName string) Option {
	return func(config *Config) error {
		config.nodeName = nodeName
		return nil
	}
}

// WithAPIAddrPort adds a APIAddr and APIPort to the configuration
func WithAPIAddrPort(addr string, port int) Option {
	return func(config *Config) error {
		config.apiAddr = addr
		config.apiPort = port
		return nil
	}
}

// WithBindAddrPort adds a BindAddr and BindPort to the configuration
func WithBindAddrPort(addr string, port int) Option {
	return func(config *Config) error {
		config.bindAddr = addr
		config.bindPort = port
		return nil
	}
}

// WithAdvertiseAddrPort adds a AdvertiseAddr and AdvertisePort to the configuration
func WithAdvertiseAddrPort(addr string, port int) Option {
	return func(config *Config) error {
		config.advertiseAddr = addr
		config.advertisePort = port
		return nil
	}
}

// WithClientAddrPort adds a ClientAddr and ClientPort to the configuration
func WithClientAddrPort(addr string, port int) Option {
	return func(config *Config) error {
		config.clientAddr = addr
		config.clientPort = port
		return nil
	}
}

// WithExisting adds a Existing to the configuration
func WithExisting(existing []string) Option {
	return func(config *Config) error {
		config.existing = existing
		return nil
	}
}

// WithLogOutput adds a LogOutput to the configuration
func WithLogOutput(logOutput io.Writer) Option {
	return func(config *Config) error {
		config.logOutput = logOutput
		return nil
	}
}

// WithBroadcastTimeout adds a BroadcastTimeout to the configuration
func WithBroadcastTimeout(d time.Duration) Option {
	return func(config *Config) error {
		config.broadcastTimeout = d
		return nil
	}
}

// PeerInfo describes what each peer is, along with the addr and port of each
type PeerInfo struct {
	Name     string
	PeerType PeerType
	APIAddr  string
	APIPort  int
}

// encodeTagPeerInfo encodes the peer information for the node tags.
func encodePeerInfoTag(info PeerInfo) map[string]string {
	return map[string]string{
		"name":      info.Name,
		PeerTypeTag: info.PeerType.String(),
		"api_addr":  info.APIAddr,
		"api_port":  strconv.Itoa(info.APIPort),
	}
}

// decodePeerInfoTag gets the peer information from the node tags.
func decodePeerInfoTag(m map[string]string) (info PeerInfo, err error) {
	name, ok := m["name"]
	if !ok {
		err = errors.Errorf("missing name")
		return
	}
	info.Name = name

	peerType, ok := m[PeerTypeTag]
	if !ok {
		err = errors.Errorf("missing %s", PeerTypeTag)
		return
	}
	info.PeerType = PeerType(peerType)

	apiPort, ok := m["api_port"]
	if !ok {
		err = errors.Errorf("missing api_port")
		return
	}
	if info.APIPort, err = strconv.Atoi(apiPort); err != nil {
		return
	}

	if info.APIAddr, ok = m["api_addr"]; !ok {
		err = errors.Errorf("missing api_addr")
		return
	}

	return
}
