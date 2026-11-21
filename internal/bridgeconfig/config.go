// Package bridgeconfig defines the private stdin protocol between Slacker
// and its companion compiled against the pinned XTLS sources.
package bridgeconfig

type Config struct {
	Mode    string
	Address string
	Name    string
	Target  string
	Backend string
	Key     []byte
	ShortID []byte
}
